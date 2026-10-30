//go:build linux
// +build linux

package service

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

const (
	dhcpOpRequest = 1
	dhcpOpReply   = 2
	dhcpMsgDiscover = 1
	dhcpMsgOffer    = 2
	dhcpMsgRequest  = 3
	dhcpMsgAck      = 5
)

type dhcp4Lease struct {
	IP     net.IP
	Mask   net.IPMask
	Router net.IP
	Server net.IP
	Lease  time.Duration
}

var (
	staDhcp4Mu   sync.Mutex
	staDhcp4Stop = map[string]chan struct{}{}
)

func dhcp4Cookie() []byte { return []byte{99, 130, 83, 99} }

func dhcp4Xid() uint32 {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return uint32(time.Now().UnixNano())
	}
	return binary.BigEndian.Uint32(b[:])
}

func dhcp4Build(msgType byte, xid uint32, chaddr net.HardwareAddr, ciaddr, reqIP, serverID net.IP) []byte {
	buf := make([]byte, 240, 300)
	buf[0] = dhcpOpRequest
	buf[1] = 1
	buf[2] = 6
	binary.BigEndian.PutUint32(buf[4:], xid)
	buf[10] = 0x80 // broadcast
	if v4 := ciaddr.To4(); v4 != nil {
		copy(buf[12:16], v4)
	}
	copy(buf[28:], chaddr)
	copy(buf[236:240], dhcp4Cookie())
	buf = append(buf, 53, 1, msgType)
	if v4 := reqIP.To4(); v4 != nil {
		buf = append(buf, 50, 4)
		buf = append(buf, v4...)
	}
	if v4 := serverID.To4(); v4 != nil {
		buf = append(buf, 54, 4)
		buf = append(buf, v4...)
	}
	if hn, err := os.Hostname(); err == nil && hn != "" && len(hn) < 64 {
		buf = append(buf, 12, byte(len(hn)))
		buf = append(buf, []byte(hn)...)
	}
	buf = append(buf, 55, 4, 1, 3, 6, 51)
	buf = append(buf, 255)
	for len(buf) < 300 {
		buf = append(buf, 0)
	}
	return buf
}

func dhcp4Parse(pkt []byte, wantXid uint32, wantType byte) (*dhcp4Lease, bool) {
	if len(pkt) < 240 || pkt[0] != dhcpOpReply {
		return nil, false
	}
	if binary.BigEndian.Uint32(pkt[4:8]) != wantXid {
		return nil, false
	}
	if string(pkt[236:240]) != string(dhcp4Cookie()) {
		return nil, false
	}
	l := &dhcp4Lease{
		IP:    append(net.IP(nil), pkt[16:20]...),
		Mask:  net.CIDRMask(24, 32),
		Lease: time.Hour,
	}
	if l.IP.Equal(net.IPv4zero) {
		return nil, false
	}
	i := 240
	gotType := byte(0)
	for i < len(pkt) {
		opt := pkt[i]
		i++
		if opt == 0 {
			continue
		}
		if opt == 255 {
			break
		}
		if i >= len(pkt) {
			break
		}
		n := int(pkt[i])
		i++
		if i+n > len(pkt) {
			break
		}
		val := pkt[i : i+n]
		i += n
		switch opt {
		case 53:
			if n >= 1 {
				gotType = val[0]
			}
		case 1:
			if n >= 4 {
				l.Mask = net.IPv4Mask(val[0], val[1], val[2], val[3])
			}
		case 3:
			if n >= 4 {
				l.Router = append(net.IP(nil), val[:4]...)
			}
		case 54:
			if n >= 4 {
				l.Server = append(net.IP(nil), val[:4]...)
			}
		case 51:
			if n >= 4 {
				sec := binary.BigEndian.Uint32(val[:4])
				if sec > 0 {
					l.Lease = time.Duration(sec) * time.Second
				}
			}
		}
	}
	if gotType != wantType {
		return nil, false
	}
	return l, true
}

func dhcp4Conn(ifName string) (net.PacketConn, error) {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM, unix.IPPROTO_UDP)
	if err != nil {
		return nil, err
	}
	if err = unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_BROADCAST, 1); err != nil {
		unix.Close(fd)
		return nil, err
	}
	_ = unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_REUSEADDR, 1)
	if err = unix.BindToDevice(fd, ifName); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("bind %s: %w", ifName, err)
	}
	if err = unix.Bind(fd, &unix.SockaddrInet4{Port: 68}); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("bind :68: %w", err)
	}
	f := os.NewFile(uintptr(fd), "dhcp4-"+ifName)
	pc, err := net.FilePacketConn(f)
	_ = f.Close()
	if err != nil {
		return nil, err
	}
	return pc, nil
}

func dhcp4RoundTrip(ifName string, mac net.HardwareAddr, xid uint32, msgType, wantType byte, ciaddr, reqIP, serverID net.IP, wait time.Duration) (*dhcp4Lease, error) {
	pc, err := dhcp4Conn(ifName)
	if err != nil {
		return nil, err
	}
	defer pc.Close()
	dst := &net.UDPAddr{IP: net.IPv4bcast, Port: 67}
	pkt := dhcp4Build(msgType, xid, mac, ciaddr, reqIP, serverID)
	if _, err = pc.WriteTo(pkt, dst); err != nil {
		return nil, err
	}
	buf := make([]byte, 1500)
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		_ = pc.SetReadDeadline(time.Now().Add(time.Until(deadline)))
		n, _, err := pc.ReadFrom(buf)
		if err != nil {
			if nerr, ok := err.(net.Error); ok && nerr.Timeout() {
				break
			}
			continue
		}
		if l, ok := dhcp4Parse(buf[:n], xid, wantType); ok {
			return l, nil
		}
	}
	return nil, fmt.Errorf("DHCP timeout waiting for message type %d", wantType)
}

func dhcp4DiscoverAndRequest(ifName string) (*dhcp4Lease, error) {
	ifi, err := net.InterfaceByName(ifName)
	if err != nil {
		return nil, err
	}
	mac := ifi.HardwareAddr
	if len(mac) < 6 {
		return nil, fmt.Errorf("%s has no MAC address", ifName)
	}
	var lastErr error
	for try := 0; try < 4; try++ {
		xid := dhcp4Xid()
		offer, err := dhcp4RoundTrip(ifName, mac, xid, dhcpMsgDiscover, dhcpMsgOffer, nil, nil, nil, 3*time.Second)
		if err != nil {
			lastErr = err
			continue
		}
		ack, err := dhcp4RoundTrip(ifName, mac, xid, dhcpMsgRequest, dhcpMsgAck, nil, offer.IP, offer.Server, 3*time.Second)
		if err != nil {
			lastErr = err
			continue
		}
		if ack.Router == nil {
			ack.Router = offer.Router
		}
		if ack.Server == nil {
			ack.Server = offer.Server
		}
		return ack, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no DHCP offer")
	}
	return nil, lastErr
}

func applyDhcp4Lease(ifName string, l *dhcp4Lease) error {
	ones, _ := l.Mask.Size()
	if ones == 0 {
		ones = 24
	}
	runQuiet("ip", "-4", "addr", "flush", "dev", ifName)
	cidr := fmt.Sprintf("%s/%d", l.IP.String(), ones)
	cmdOut, err := runQuietErr("ip", "addr", "add", cidr, "brd", "+", "dev", ifName)
	if err != nil {
		return fmt.Errorf("ip addr add %s: %v %s", cidr, err, cmdOut)
	}
	runQuiet("ip", "link", "set", ifName, "up")
	if l.Router != nil && !l.Router.IsUnspecified() {
		runQuiet("ip", "route", "replace", "default", "via", l.Router.String(), "dev", ifName)
	}
	log.Printf("dhcp4 %s: %s via %s (lease %s)", ifName, cidr, l.Router, l.Lease)
	return nil
}

func runQuietErr(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func startDhcp4Builtin(ifName string) error {
	stopDhcp4Builtin(ifName)
	l, err := dhcp4DiscoverAndRequest(ifName)
	if err != nil {
		return err
	}
	if err = applyDhcp4Lease(ifName, l); err != nil {
		return err
	}
	stop := make(chan struct{})
	staDhcp4Mu.Lock()
	staDhcp4Stop[ifName] = stop
	staDhcp4Mu.Unlock()
	go dhcp4RenewLoop(ifName, l, stop)
	return nil
}

func dhcp4RenewLoop(ifName string, l *dhcp4Lease, stop chan struct{}) {
	wait := l.Lease / 2
	if wait < 30*time.Second {
		wait = 30 * time.Second
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			nl, err := dhcp4DiscoverAndRequest(ifName)
			if err != nil {
				log.Printf("dhcp4 %s renew: %v", ifName, err)
				t.Reset(30 * time.Second)
				continue
			}
			if err = applyDhcp4Lease(ifName, nl); err != nil {
				log.Printf("dhcp4 %s apply renew: %v", ifName, err)
				t.Reset(30 * time.Second)
				continue
			}
			l = nl
			wait = l.Lease / 2
			if wait < 30*time.Second {
				wait = 30 * time.Second
			}
			t.Reset(wait)
		}
	}
}

func stopDhcp4Builtin(ifName string) {
	staDhcp4Mu.Lock()
	if ch, ok := staDhcp4Stop[ifName]; ok {
		close(ch)
		delete(staDhcp4Stop, ifName)
	}
	staDhcp4Mu.Unlock()
}
