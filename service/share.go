//go:build linux
// +build linux

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mame82/P4wnP1_aloa/common"
)

const (
	shareNatChain    = "P4WNP1SHARE"
	shareWgIface     = "wg0"
	shareOvpnIface   = "tun0"
	shareOvpnPidFile = "/run/p4wnp1-openvpn.pid"
	shareOvpnLogFile = "/tmp/p4wnp1-openvpn.log"
)

type ShareManager struct {
	root         *Service
	mu           sync.Mutex
	sharing      bool
	viaVPN       bool
	uplink       string
	vpnKind      string
	vpnFile      string
	ovpnCmd      *exec.Cmd
	vpnStarting  bool
	vpnUserStop  bool
	watchStarted bool
}

type shareStatus struct {
	Sharing      bool     `json:"sharing"`
	ViaVPN       bool     `json:"via_vpn"`
	Uplink       string   `json:"uplink"`
	UplinkIP     string   `json:"uplink_ip"`
	Wlan0Present bool     `json:"wlan0_present"`
	Wlan0IP      string   `json:"wlan0_ip"`
	Wlan0IsAP    bool     `json:"wlan0_is_ap"`
	Wlan1Present bool     `json:"wlan1_present"`
	Wlan1IP      string   `json:"wlan1_ip"`
	UsbethIP     string   `json:"usbeth_ip"`
	VpnRunning   bool     `json:"vpn_running"`
	VpnKind      string   `json:"vpn_kind"`
	VpnIface     string   `json:"vpn_iface"`
	VpnIP        string   `json:"vpn_ip"`
	VpnFile      string   `json:"vpn_file"`
	VpnDir       string   `json:"vpn_dir"`
	WgBin        string   `json:"wg_bin"`
	OvpnBin      string   `json:"openvpn_bin"`
	HasConfig       bool     `json:"has_config"`
	USBWifi         []string `json:"usb_wifi"`
	Note            string   `json:"note,omitempty"`
	BootVPN         bool     `json:"boot_vpn"`
	BootVPNKind     string   `json:"boot_vpn_kind"`
	BootVPNName     string   `json:"boot_vpn_name"`
	BootShare       bool     `json:"boot_share"`
	BootShareViaVPN bool     `json:"boot_share_via_vpn"`
	DnsMode         string   `json:"dns_mode"`
	Dns             string   `json:"dns"`
	DnsActive       string   `json:"dns_active,omitempty"`
}

type shareBootCfg struct {
	VPNEnable   bool   `json:"vpn_enable"`
	VPNKind     string `json:"vpn_kind"`
	VPNName     string `json:"vpn_name"`
	ShareEnable bool   `json:"share_enable"`
	ShareViaVPN bool   `json:"share_via_vpn"`
	DNSMode     string `json:"dns_mode"`
	DNS         string `json:"dns"`
}

const shareDefaultDNS = "8.8.8.8,1.1.1.1"

func shareBootPath() string {
	return filepath.Join(shareVpnDir(), ".boot.json")
}

func (sm *ShareManager) LoadBoot() shareBootCfg {
	b, err := ioutil.ReadFile(shareBootPath())
	if err != nil {
		return shareBootCfg{DNSMode: "default", DNS: shareDefaultDNS}
	}
	var cfg shareBootCfg
	if json.Unmarshal(b, &cfg) != nil {
		return shareBootCfg{DNSMode: "default", DNS: shareDefaultDNS}
	}
	return normalizeShareDNS(cfg)
}

func parseDNSList(s string) ([]string, error) {
	s = strings.ReplaceAll(s, ";", ",")
	var out []string
	seen := map[string]bool{}
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		ip := net.ParseIP(p)
		if ip == nil || ip.To4() == nil {
			return nil, fmt.Errorf("invalid DNS address %q (IPv4 only)", p)
		}
		v := ip.To4().String()
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
		if len(out) == 4 {
			break
		}
	}
	return out, nil
}

func normalizeShareDNS(cfg shareBootCfg) shareBootCfg {
	mode := strings.ToLower(strings.TrimSpace(cfg.DNSMode))
	switch mode {
	case "off", "custom":
		cfg.DNSMode = mode
	default:
		cfg.DNSMode = "default"
	}
	if strings.TrimSpace(cfg.DNS) == "" {
		cfg.DNS = shareDefaultDNS
	}
	if cfg.DNSMode == "custom" {
		if list, err := parseDNSList(cfg.DNS); err == nil && len(list) > 0 {
			cfg.DNS = strings.Join(list, ",")
		}
	}
	return cfg
}

func (sm *ShareManager) dnsServers() (mode string, servers []string) {
	cfg := sm.LoadBoot()
	mode = cfg.DNSMode
	if mode == "off" {
		return mode, nil
	}
	if mode == "custom" {
		list, err := parseDNSList(cfg.DNS)
		if err == nil && len(list) > 0 {
			return mode, list
		}
	}
	return "default", []string{"8.8.8.8", "1.1.1.1"}
}

func (sm *ShareManager) SaveBoot(cfg shareBootCfg) error {
	if err := sm.ensureVpnDir(); err != nil {
		return err
	}
	cfg.VPNKind = strings.TrimSpace(cfg.VPNKind)
	cfg.VPNName = strings.TrimSpace(cfg.VPNName)
	if cfg.VPNEnable && cfg.VPNName == "" {
		return fmt.Errorf("select a saved VPN file before enabling connect at boot")
	}
	cfg = normalizeShareDNS(cfg)
	if cfg.DNSMode == "custom" {
		list, err := parseDNSList(cfg.DNS)
		if err != nil {
			return err
		}
		if len(list) == 0 {
			return fmt.Errorf("enter at least one IPv4 DNS server, for example 8.8.8.8,1.1.1.1")
		}
		cfg.DNS = strings.Join(list, ",")
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := ioutil.WriteFile(shareBootPath(), b, 0600); err != nil {
		return err
	}
	log.Printf("share: settings saved (vpn=%v %s share=%v dns=%s %s)", cfg.VPNEnable, cfg.VPNName, cfg.ShareEnable, cfg.DNSMode, cfg.DNS)
	sm.mu.Lock()
	sharing := sm.sharing
	up := sm.uplink
	sm.mu.Unlock()
	if sharing && up != "" {
		sm.mu.Lock()
		_ = sm.applyNAT(up)
		sm.mu.Unlock()
	}
	return nil
}

func (sm *ShareManager) ApplyBootConfig() {
	cfg := sm.LoadBoot()
	if !cfg.VPNEnable && !cfg.ShareEnable {
		log.Printf("boot: no VPN/share autostart configured")
		return
	}
	sm.ensureWatch()

	// Bring up WiFi-client NAT first (wlan0 STA or wlan1) so USB keeps working
	// even if the VPN handshake is slow or fails.
	if cfg.ShareEnable {
		var err error
		for i := 0; i < 3; i++ {
			err = sm.StartShare(false)
			if err == nil {
				break
			}
			log.Printf("boot: share via WiFi attempt %d: %v", i+1, err)
			time.Sleep(3 * time.Second)
		}
		if err != nil {
			log.Printf("boot: share via WiFi failed: %v", err)
		} else {
			log.Printf("boot: internet share started via WiFi uplink")
		}
	}
	if !cfg.VPNEnable || cfg.VPNName == "" {
		return
	}
	sm.mu.Lock()
	sm.vpnUserStop = false
	sm.mu.Unlock()
	up := pickWifiUplink()
	if up == "" {
		log.Printf("boot: no WiFi uplink yet; VPN will keep retrying in background")
	} else if !waitUplinkInternet(up, 25*time.Second) {
		log.Printf("boot: %s is not reaching the internet yet; VPN will keep retrying", up)
	}
	kind := inferVpnKind(cfg.VPNKind, cfg.VPNName)
	log.Printf("boot: starting VPN %s (%s)", cfg.VPNName, kind)
	if err := sm.StartVPN(kind, cfg.VPNName, "", "", ""); err != nil {
		log.Printf("boot: first VPN attempt failed: %v (will retry in background; WiFi share stays up)", err)
	} else if vpnTunnelReady(sm) && cfg.ShareEnable {
		if err := sm.StartShare(true); err != nil {
			log.Printf("boot: share via VPN: %v (keeping WiFi share)", err)
		} else {
			log.Printf("boot: internet share switched to VPN")
		}
	} else {
		log.Printf("boot: VPN process started; waiting for handshake before switching share")
	}
}

func waitUplinkInternet(iface string, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if uplinkCanReachInternet(iface) {
			return true
		}
		time.Sleep(2 * time.Second)
	}
	return uplinkCanReachInternet(iface)
}

func uplinkCanReachInternet(iface string) bool {
	if iface == "" || ifaceIPv4(iface) == "" {
		return false
	}
	out, err := exec.Command("ip", "-4", "route", "get", "1.1.1.1").CombinedOutput()
	if err == nil && strings.Contains(string(out), " dev "+iface) {
		cmd := exec.Command("ping", "-I", iface, "-c", "1", "-W", "2", "1.1.1.1")
		if cmd.Run() == nil {
			return true
		}
		cmd = exec.Command("ping", "-I", iface, "-c", "1", "-W", "2", "8.8.8.8")
		if cmd.Run() == nil {
			return true
		}
		// Route exists even if ICMP is blocked.
		return true
	}
	cmd := exec.Command("ping", "-I", iface, "-c", "1", "-W", "2", "8.8.8.8")
	return cmd.Run() == nil
}

func wgHasHandshake() bool {
	bin := wgBin()
	if bin == "" {
		return CheckInterfaceExistence(shareWgIface)
	}
	out, err := exec.Command(bin, "show", shareWgIface, "latest-handshakes").Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		ts := f[len(f)-1]
		if ts != "" && ts != "0" {
			return true
		}
	}
	return false
}

func inferVpnKind(kind, name string) string {
	lower := strings.ToLower(name)
	if strings.HasSuffix(lower, ".ovpn") {
		return "openvpn"
	}
	if strings.HasSuffix(lower, ".conf") {
		return "wireguard"
	}
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "openvpn" || kind == "wireguard" {
		return kind
	}
	return "wireguard"
}

func vpnTunnelReady(sm *ShareManager) bool {
	sm.mu.Lock()
	kind := sm.vpnKind
	iface := sm.vpnIfaceLocked()
	up := sm.vpnRunningLocked() && iface != ""
	sm.mu.Unlock()
	if !up {
		return false
	}
	if kind == "wireguard" {
		return wgHasHandshake()
	}
	return ifaceIPv4(iface) != ""
}

func lookupIPv4(host string) string {
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if host == "" {
		return ""
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			return ip4.String()
		}
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{Timeout: 3 * time.Second}
			return d.DialContext(ctx, "udp", "8.8.8.8:53")
		},
	}
	ips, err := r.LookupIP(ctx, "ip4", host)
	if err != nil || len(ips) == 0 {
		ips, err = net.LookupIP(host)
	}
	if err != nil {
		return ""
	}
	for _, ip := range ips {
		if ip4 := ip.To4(); ip4 != nil {
			return ip4.String()
		}
	}
	return ""
}

func rewriteWgEndpoints(cfg string) string {
	var out []string
	for _, line := range strings.Split(cfg, "\n") {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			out = append(out, line)
			continue
		}
		eq := strings.IndexByte(trim, '=')
		if eq < 0 {
			out = append(out, line)
			continue
		}
		key := strings.ToLower(strings.TrimSpace(trim[:eq]))
		if key != "endpoint" {
			out = append(out, line)
			continue
		}
		origKey := strings.TrimSpace(trim[:eq])
		val := strings.TrimSpace(trim[eq+1:])
		host, port, err := net.SplitHostPort(val)
		if err != nil {
			out = append(out, line)
			continue
		}
		ip := lookupIPv4(host)
		if ip == "" || ip == host {
			out = append(out, line)
			continue
		}
		log.Printf("share: VPN endpoint %s -> %s", host, ip)
		out = append(out, origKey+" = "+net.JoinHostPort(ip, port))
	}
	return strings.Join(out, "\n")
}

func rewriteOvpnRemotes(cfg string) string {
	var out []string
	for _, line := range strings.Split(cfg, "\n") {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") || strings.HasPrefix(trim, ";") {
			out = append(out, line)
			continue
		}
		fields := strings.Fields(trim)
		if len(fields) < 2 || strings.ToLower(fields[0]) != "remote" {
			out = append(out, line)
			continue
		}
		host := fields[1]
		ip := lookupIPv4(host)
		if ip == "" || ip == host {
			out = append(out, line)
			continue
		}
		log.Printf("share: OpenVPN remote %s -> %s", host, ip)
		fields[1] = ip
		out = append(out, strings.Join(fields, " "))
	}
	return strings.Join(out, "\n")
}

func NewShareManager(root *Service) *ShareManager {
	sm := &ShareManager{root: root}
	sm.ensureWatch()
	return sm
}

func (sm *ShareManager) ensureWatch() {
	if sm == nil {
		return
	}
	sm.mu.Lock()
	if sm.watchStarted {
		sm.mu.Unlock()
		return
	}
	sm.watchStarted = true
	sm.mu.Unlock()
	go sm.shareWatch()
}

func (sm *ShareManager) shareWatch() {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for range t.C {
		cfg := sm.LoadBoot()
		sm.mu.Lock()
		sharing := sm.sharing
		via := sm.viaVPN
		up := sm.uplink
		vpnUp := sm.vpnRunningLocked()
		starting := sm.vpnStarting
		userStop := sm.vpnUserStop
		sm.mu.Unlock()

		if sharing && via && !vpnUp {
			log.Printf("share: VPN dropped, falling back to WiFi uplink")
			_ = sm.StartShare(false)
			sharing = true
			via = false
		}
		if sharing && !via && up != "" && ifaceIPv4(up) == "" {
			if alt := pickWifiUplink(); alt != "" {
				log.Printf("share: uplink %s lost its address, re-applying via %s", up, alt)
				_ = sm.StartShare(false)
			}
		}

		if !cfg.VPNEnable || cfg.VPNName == "" || userStop || starting {
			continue
		}
		if !vpnUp {
			if pickWifiUplink() == "" {
				continue
			}
			kind := inferVpnKind(cfg.VPNKind, cfg.VPNName)
			log.Printf("share: boot VPN %s is not running, starting", cfg.VPNName)
			if err := sm.StartVPN(kind, cfg.VPNName, "", "", ""); err != nil {
				log.Printf("share: boot VPN retry: %v", err)
			}
			continue
		}
		if sharing && !via && vpnTunnelReady(sm) {
			log.Printf("share: VPN handshake ok, switching NAT to the tunnel")
			if err := sm.StartShare(true); err != nil {
				log.Printf("share: switch to VPN: %v", err)
			}
		}
	}
}

func (sm *ShareManager) OnUSBHostChanged(connected bool) {
	if !connected {
		return
	}
	go func() {
		time.Sleep(1500 * time.Millisecond)
		sm.mu.Lock()
		sharing := sm.sharing
		uplink := sm.uplink
		sm.mu.Unlock()
		if !sharing || uplink == "" {
			return
		}
		log.Printf("share: USB host connected, re-applying NAT via %s", uplink)
		sm.mu.Lock()
		_ = sm.applyNAT(uplink)
		sm.mu.Unlock()
	}()
}

func shareVpnDir() string {
	return filepath.Join(common.PATH_ROOT, "vpns")
}

func shareWgRuntimePath() string {
	return filepath.Join(shareVpnDir(), ".runtime", "wg0.conf")
}

func wgQuickBin() string { return lookPathBin("wg-quick") }
func wgBin() string      { return lookPathBin("wg") }
func openvpnBin() string { return lookPathBin("openvpn") }

func detectVpnKind(cfg string) string {
	s := strings.ToLower(cfg)
	if strings.Contains(s, "[interface]") && strings.Contains(s, "privatekey") {
		return "wireguard"
	}
	if strings.Contains(s, "remote ") || strings.Contains(cfg, "client") || strings.Contains(s, "dev tun") {
		return "openvpn"
	}
	return ""
}

func (sm *ShareManager) vpnIfaceLocked() string {
	for _, n := range []string{shareWgIface, shareOvpnIface, "tun1"} {
		if CheckInterfaceExistence(n) {
			return n
		}
	}
	return ""
}

func (sm *ShareManager) vpnRunningLocked() bool {
	if wgBin() != "" && exec.Command(wgBin(), "show", shareWgIface).Run() == nil {
		return true
	}
	if _, err := os.Stat(shareOvpnPidFile); err == nil {
		b, err := ioutil.ReadFile(shareOvpnPidFile)
		if err == nil {
			pid := strings.TrimSpace(string(b))
			if pid != "" {
				if err := syscall.Kill(atoiSafe(pid), 0); err == nil {
					return true
				}
			}
		}
	}
	return sm.vpnIfaceLocked() != ""
}

func atoiSafe(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}

func (sm *ShareManager) applyNAT(uplink string) error {
	sm.flushNAT()
	if uplink == "" {
		sm.sharing = false
		sm.uplink = ""
		return nil
	}
	if !CheckInterfaceExistence(uplink) {
		return fmt.Errorf("uplink %s is not present", uplink)
	}
	runQuiet("modprobe", "iptable_nat")
	runQuiet("modprobe", "xt_MASQUERADE")
	runQuiet("sysctl", "-w", "net.ipv4.ip_forward=1")
	runQuiet("sysctl", "-w", "net.ipv4.conf.all.forwarding=1")
	runQuiet("sysctl", "-w", "net.ipv4.conf.all.rp_filter=2")
	for _, n := range []string{uplink, "usbeth", "usb0", "usb1", "wlan0"} {
		runQuiet("sysctl", "-w", "net.ipv4.conf."+n+".rp_filter=2")
		runQuiet("sysctl", "-w", "net.ipv4.conf."+n+".forwarding=1")
	}
	_ = iptablesCmd("-P", "FORWARD", "ACCEPT")
	iptablesEnsureChain("nat", shareNatChain)
	iptablesEnsureChain("nat", shareNatChain+"DNS")
	iptablesEnsureChain("filter", shareNatChain)
	iptablesEnsureJump("nat", "POSTROUTING", shareNatChain)
	iptablesEnsureJump("nat", "PREROUTING", shareNatChain+"DNS")
	iptablesEnsureJump("filter", "FORWARD", shareNatChain)
	if err := iptablesCmd("-t", "nat", "-A", shareNatChain, "-o", uplink, "-j", "MASQUERADE"); err != nil {
		return fmt.Errorf("masquerade %s: %v", uplink, err)
	}
	_ = iptablesCmd("-t", "nat", "-A", shareNatChain, "-s", "172.16.0.0/30", "-o", uplink, "-j", "MASQUERADE")
	_ = iptablesCmd("-t", "nat", "-A", shareNatChain, "-s", "172.24.0.0/24", "-o", uplink, "-j", "MASQUERADE")
	mode, servers := sm.dnsServers()
	if len(servers) > 0 {
		dest := servers[0] + ":53"
		for _, proto := range []string{"udp", "tcp"} {
			for _, lan := range shareLanIfaces(uplink) {
				_ = iptablesCmd("-t", "nat", "-A", shareNatChain+"DNS", "-i", lan, "-p", proto, "--dport", "53", "-j", "DNAT", "--to-destination", dest)
			}
		}
	}
	lans := shareLanIfaces(uplink)
	for _, lan := range lans {
		_ = iptablesCmd("-A", shareNatChain, "-i", lan, "-o", uplink, "-j", "ACCEPT")
		_ = iptablesCmd("-A", shareNatChain, "-i", uplink, "-o", lan, "-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-j", "ACCEPT")
		_ = iptablesCmd("-A", shareNatChain, "-i", uplink, "-o", lan, "-m", "state", "--state", "RELATED,ESTABLISHED", "-j", "ACCEPT")
	}
	_ = iptablesCmd("-A", shareNatChain, "-p", "tcp", "--tcp-flags", "SYN,RST", "SYN", "-j", "TCPMSS", "--clamp-mss-to-pmtu")
	sm.ensureUsbethShareDHCP()
	if uplink != "wlan0" && uplink != wifi_if_name {
		sm.ensureApShareDHCP()
	}
	sm.sharing = true
	sm.uplink = uplink
	if mode == "off" {
		log.Printf("share: NAT %v out %s (DNS override off)", lans, uplink)
	} else {
		log.Printf("share: NAT %v out %s (DNS %s via %s)", lans, uplink, strings.Join(servers, ","), mode)
	}
	return nil
}

// shareLanIfaces are the interfaces that receive shared internet.
// Never include the uplink itself (e.g. wlan0 STA must not be listed as a LAN).
func shareLanIfaces(uplink string) []string {
	lans := []string{"usbeth", "usb0", "usb1"}
	if uplink != "wlan0" && uplink != wifi_if_name {
		lans = append(lans, "wlan0")
	}
	return lans
}

func rewriteDnsmasqUpstream(path string, servers []string) {
	b, err := ioutil.ReadFile(path)
	if err != nil {
		return
	}
	var keep []string
	for _, line := range strings.Split(string(b), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "server=") || t == "no-resolv" {
			continue
		}
		keep = append(keep, line)
	}
	for len(keep) > 0 && keep[len(keep)-1] == "" {
		keep = keep[:len(keep)-1]
	}
	if len(servers) > 0 {
		keep = append(keep, "no-resolv")
		for _, s := range servers {
			keep = append(keep, "server="+s)
		}
	}
	_ = ioutil.WriteFile(path, []byte(strings.Join(keep, "\n")+"\n"), 0644)
}

func (sm *ShareManager) applyShareDHCP(ifName, gw string) {
	if sm.root == nil || sm.root.SubSysNetwork == nil || ifName == "" {
		return
	}
	nim, err := sm.root.SubSysNetwork.GetManagedInterface(ifName)
	if err != nil || nim == nil || nim.state == nil || nim.state.CurrentSettings == nil {
		return
	}
	s := nim.state.CurrentSettings
	if s.DhcpServerSettings == nil {
		return
	}
	if s.DhcpServerSettings.Options == nil {
		s.DhcpServerSettings.Options = map[uint32]string{}
	}
	mode, servers := sm.dnsServers()
	wantDNS := ""
	if mode == "off" {
		wantDNS = gw
	} else {
		wantDNS = strings.Join(servers, ",")
	}
	changed := false
	if gw != "" && s.DhcpServerSettings.Options[3] != gw {
		s.DhcpServerSettings.Options[3] = gw
		changed = true
	}
	if s.DhcpServerSettings.Options[6] != wantDNS {
		s.DhcpServerSettings.Options[6] = wantDNS
		changed = true
	}
	if s.DhcpServerSettings.ListenPort == 0 {
		s.DhcpServerSettings.ListenPort = 53
		changed = true
	}
	confName := NameConfigFileDHCPSrv(ifName)
	if !changed {
		if _, err := os.Stat(confName); err == nil {
			return
		}
	}
	if err := DHCPCreateConfigFile(s.DhcpServerSettings, confName); err != nil {
		log.Printf("share: %s DHCP config: %v", ifName, err)
		return
	}
	if mode == "off" {
		rewriteDnsmasqUpstream(confName, nil)
	} else {
		rewriteDnsmasqUpstream(confName, servers)
	}
	_ = nim.StopDHCPServer()
	if err := nim.StartDHCPServer(confName); err != nil {
		log.Printf("share: %s DHCP restart: %v", ifName, err)
		return
	}
	if mode == "off" {
		log.Printf("share: %s DHCP advertises gateway %s, DNS override off", ifName, gw)
	} else {
		log.Printf("share: %s DHCP advertises gateway %s and DNS %s", ifName, gw, wantDNS)
	}
}

func (sm *ShareManager) ensureUsbethShareDHCP() {
	sm.applyShareDHCP(USB_ETHERNET_BRIDGE_NAME, "172.16.0.1")
}

func (sm *ShareManager) ensureApShareDHCP() {
	gw := "172.24.0.1"
	if sm.root != nil && sm.root.SubSysNetwork != nil {
		if nim, err := sm.root.SubSysNetwork.GetManagedInterface(wifi_if_name); err == nil && nim != nil && nim.state != nil && nim.state.CurrentSettings != nil && nim.state.CurrentSettings.IpAddress4 != "" {
			gw = nim.state.CurrentSettings.IpAddress4
		}
	}
	sm.applyShareDHCP(wifi_if_name, gw)
}

func (sm *ShareManager) flushNAT() {
	_ = iptablesCmd("-t", "nat", "-F", shareNatChain)
	_ = iptablesCmd("-t", "nat", "-F", shareNatChain+"DNS")
	_ = iptablesCmd("-F", shareNatChain)
}

func (sm *ShareManager) StartShare(viaVPN bool) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if viaVPN {
		if !sm.vpnRunningLocked() {
			return fmt.Errorf("connect the VPN first, or start share without VPN")
		}
		up := sm.vpnIfaceLocked()
		if up == "" {
			return fmt.Errorf("VPN is up but no tunnel interface (wg0/tun0) was found")
		}
		sm.viaVPN = true
		return sm.applyNAT(up)
	}
	up := pickWifiUplink()
	if up == "" {
		if CheckInterfaceExistence("wlan1") || len(listUSBWifiIfaces()) > 0 {
			return fmt.Errorf("no WiFi client uplink. Join a network on wlan0 (Client/Failover) or wlan1 first")
		}
		return fmt.Errorf("no WiFi client uplink. Join a network on the WiFi page (wlan0 client or USB wlan1)")
	}
	sm.viaVPN = false
	return sm.applyNAT(up)
}

func (sm *ShareManager) StopShare() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.flushNAT()
	sm.sharing = false
	sm.viaVPN = false
	sm.uplink = ""
	log.Printf("share: internet passthrough stopped")
	return nil
}

func vpnKindExt(kind string) string {
	if kind == "openvpn" {
		return ".ovpn"
	}
	return ".conf"
}

func sanitizeVpnName(name, kind string) (string, error) {
	name = strings.TrimSpace(name)
	name = filepath.Base(name)
	name = strings.ReplaceAll(name, " ", "_")
	extWant := vpnKindExt(kind)
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".ovpn"):
		name = name[:len(name)-5]
	case strings.HasSuffix(lower, ".conf"):
		name = name[:len(name)-5]
	}
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		}
	}
	s := strings.Trim(b.String(), ".")
	if s == "" || s == "." || s == ".." {
		return "", fmt.Errorf("enter a file name (letters, numbers, dash, underscore)")
	}
	return s + extWant, nil
}

func (sm *ShareManager) ensureVpnDir() error {
	if err := os.MkdirAll(shareVpnDir(), 0700); err != nil {
		return err
	}
	sm.migrateOldVpn()
	return nil
}

func (sm *ShareManager) migrateOldVpn() {
	oldDir := filepath.Join(common.PATH_ROOT, "vpn")
	pairs := [][2]string{
		{filepath.Join(oldDir, "wg0.conf"), filepath.Join(shareVpnDir(), "wg0.conf")},
		{filepath.Join(oldDir, "client.ovpn"), filepath.Join(shareVpnDir(), "client.ovpn")},
	}
	for _, p := range pairs {
		if _, err := os.Stat(p[1]); err == nil {
			continue
		}
		b, err := ioutil.ReadFile(p[0])
		if err != nil {
			continue
		}
		_ = ioutil.WriteFile(p[1], b, 0600)
	}
}

func (sm *ShareManager) vpnFilePath(kind, name string) (string, error) {
	safe, err := sanitizeVpnName(name, kind)
	if err != nil {
		return "", err
	}
	path := filepath.Join(shareVpnDir(), safe)
	if filepath.Clean(filepath.Dir(path)) != filepath.Clean(shareVpnDir()) {
		return "", fmt.Errorf("invalid VPN file name")
	}
	return path, nil
}

func (sm *ShareManager) ListVPN(kind string) []string {
	_ = sm.ensureVpnDir()
	ents, err := ioutil.ReadDir(shareVpnDir())
	if err != nil {
		return []string{}
	}
	ext := ""
	switch kind {
	case "openvpn":
		ext = ".ovpn"
	case "wireguard":
		ext = ".conf"
	}
	out := []string{}
	for _, e := range ents {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		n := e.Name()
		lower := strings.ToLower(n)
		if ext != "" {
			if !strings.HasSuffix(lower, ext) {
				continue
			}
		} else if !strings.HasSuffix(lower, ".conf") && !strings.HasSuffix(lower, ".ovpn") {
			continue
		}
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func (sm *ShareManager) SaveVPN(kind, name, cfg, user, pass string) (string, error) {
	if err := sm.ensureVpnDir(); err != nil {
		return "", err
	}
	cfg = strings.ReplaceAll(cfg, "\r\n", "\n")
	cfg = strings.TrimSpace(cfg)
	if cfg == "" {
		return "", fmt.Errorf("paste a VPN config before saving")
	}
	if kind == "" || kind == "auto" {
		kind = detectVpnKind(cfg)
	}
	if kind != "wireguard" && kind != "openvpn" {
		return "", fmt.Errorf("choose WireGuard (.conf) or OpenVPN (.ovpn)")
	}
	detected := detectVpnKind(cfg)
	if detected != "" && detected != kind {
		return "", fmt.Errorf("that config looks like %s, but %s was selected", detected, kind)
	}
	path, err := sm.vpnFilePath(kind, name)
	if err != nil {
		return "", err
	}
	if err := ioutil.WriteFile(path, []byte(cfg+"\n"), 0600); err != nil {
		return "", err
	}
	if kind == "openvpn" && strings.TrimSpace(user) != "" && pass != "" {
		if err := writeOvpnAuthFile(ovpnAuthPath(path), user, pass); err != nil {
			return "", err
		}
	}
	log.Printf("share: saved VPN profile %s", filepath.Base(path))
	return filepath.Base(path), nil
}

func (sm *ShareManager) LoadVPN(kind, name string) (string, error) {
	path, err := sm.vpnFilePath(kind, name)
	if err != nil {
		return "", err
	}
	b, err := ioutil.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("could not read %s from %s", filepath.Base(path), shareVpnDir())
	}
	return string(b), nil
}

func (sm *ShareManager) DeleteVPN(kind, name string) error {
	path, err := sm.vpnFilePath(kind, name)
	if err != nil {
		return err
	}
	_ = os.Remove(ovpnAuthPath(path))
	if err := os.Remove(path); err != nil {
		return err
	}
	log.Printf("share: deleted VPN profile %s", filepath.Base(path))
	return nil
}

func stripWgDNS(cfg string) string {
	var lines []string
	for _, line := range strings.Split(cfg, "\n") {
		t := strings.ToLower(strings.TrimSpace(line))
		if strings.HasPrefix(t, "dns") && strings.Contains(t, "=") {
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func isIPv6AddrToken(tok string) bool {
	tok = strings.TrimSpace(tok)
	if tok == "" {
		return false
	}
	host := tok
	if i := strings.IndexByte(tok, '/'); i >= 0 {
		host = tok[:i]
	}
	ip := net.ParseIP(host)
	if ip != nil {
		return ip.To4() == nil
	}
	return strings.Contains(host, ":")
}

func filterIPv4CSV(val string) string {
	keep := []string{}
	for _, p := range strings.Split(val, ",") {
		p = strings.TrimSpace(p)
		if p == "" || isIPv6AddrToken(p) {
			continue
		}
		keep = append(keep, p)
	}
	return strings.Join(keep, ", ")
}

func stripWgIPv6(cfg string) string {
	var out []string
	for _, line := range strings.Split(cfg, "\n") {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			out = append(out, line)
			continue
		}
		eq := strings.IndexByte(trim, '=')
		if eq < 0 {
			out = append(out, line)
			continue
		}
		key := strings.ToLower(strings.TrimSpace(trim[:eq]))
		val := strings.TrimSpace(trim[eq+1:])
		origKey := strings.TrimSpace(trim[:eq])
		switch key {
		case "address", "allowedips", "dns":
			filtered := filterIPv4CSV(val)
			if filtered == "" {
				if key == "allowedips" {
					out = append(out, origKey+" = 0.0.0.0/0")
				}
				continue
			}
			out = append(out, origKey+" = "+filtered)
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func ovpnNeedsAuth(cfg string) bool {
	for _, line := range strings.Split(cfg, "\n") {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") || strings.HasPrefix(trim, ";") {
			continue
		}
		fields := strings.Fields(strings.ToLower(trim))
		if len(fields) > 0 && fields[0] == "auth-user-pass" {
			return true
		}
	}
	return false
}

func ovpnAuthPath(cfgPath string) string {
	return strings.TrimSuffix(cfgPath, filepath.Ext(cfgPath)) + ".auth"
}

func writeOvpnAuthFile(path, user, pass string) error {
	user = strings.TrimSpace(strings.SplitN(user, "\n", 2)[0])
	pass = strings.SplitN(pass, "\n", 2)[0]
	pass = strings.TrimRight(pass, "\r")
	if user == "" || pass == "" {
		return fmt.Errorf("OpenVPN username and password are both required")
	}
	return ioutil.WriteFile(path, []byte(user+"\n"+pass+"\n"), 0600)
}

func rewriteOvpnAuthLine(cfg, authPath string) string {
	var out []string
	replaced := false
	for _, line := range strings.Split(cfg, "\n") {
		trim := strings.TrimSpace(line)
		fields := strings.Fields(trim)
		if len(fields) > 0 && strings.ToLower(fields[0]) == "auth-user-pass" && !strings.HasPrefix(trim, "#") && !strings.HasPrefix(trim, ";") {
			out = append(out, "auth-user-pass "+authPath)
			replaced = true
			continue
		}
		out = append(out, line)
	}
	if !replaced {
		out = append(out, "auth-user-pass "+authPath)
	}
	return strings.Join(out, "\n")
}

func (sm *ShareManager) peekOvpnAuth(kind, name string) (string, bool) {
	path, err := sm.vpnFilePath(kind, name)
	if err != nil {
		return "", false
	}
	b, err := ioutil.ReadFile(ovpnAuthPath(path))
	if err != nil {
		return "", false
	}
	user := strings.TrimSpace(strings.SplitN(string(b), "\n", 2)[0])
	return user, true
}

func ovpnPidAlive() bool {
	b, err := ioutil.ReadFile(shareOvpnPidFile)
	if err != nil {
		return false
	}
	pid := atoiSafe(strings.TrimSpace(string(b)))
	if pid < 2 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

func ovpnFailReason(logText string) string {
	l := strings.ToLower(logText)
	switch {
	case strings.Contains(l, "auth_failed") || strings.Contains(l, "auth failed"):
		return "authentication failed (check username and password)"
	case strings.Contains(l, "enter auth username") || strings.Contains(l, "need username"):
		return "server asked for a username/password; enter them on this page, Save, then Connect"
	case strings.Contains(l, "private key password verification failed"):
		return "private key password failed"
	case strings.Contains(l, "exiting due to fatal error"):
		return strings.TrimSpace(logText)
	}
	return ""
}

func (sm *ShareManager) startWireGuardFile(src string) error {
	bin := wgQuickBin()
	if bin == "" {
		return fmt.Errorf("wg-quick not found (apt-get install wireguard-tools)")
	}
	b, err := ioutil.ReadFile(src)
	if err != nil {
		return err
	}
	rtDir := filepath.Join(shareVpnDir(), ".runtime")
	if err := os.MkdirAll(rtDir, 0700); err != nil {
		return err
	}
	rt := shareWgRuntimePath()
	cfg := rewriteWgEndpoints(stripWgDNS(stripWgIPv6(string(b))))
	if err := ioutil.WriteFile(rt, []byte(cfg), 0600); err != nil {
		return err
	}
	_ = exec.Command(bin, "down", rt).Run()
	_ = exec.Command(bin, "down", shareWgIface).Run()
	cmd := exec.Command(bin, "up", rt)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.ToLower(string(out) + err.Error())
		retry := false
		next := cfg
		if strings.Contains(msg, "ipv6") {
			log.Printf("share: wg-quick IPv6 failed, retrying IPv4-only")
			next = stripWgIPv6(next)
			retry = true
		}
		if strings.Contains(msg, "resolvconf") || strings.Contains(msg, "/etc/resolv") {
			log.Printf("share: wg-quick DNS helper failed, retrying without DNS= lines")
			next = stripWgDNS(next)
			retry = true
		}
		if retry {
			_ = ioutil.WriteFile(rt, []byte(next), 0600)
			_ = exec.Command(bin, "down", rt).Run()
			cmd = exec.Command(bin, "up", rt)
			out, err = cmd.CombinedOutput()
		}
	}
	if len(out) > 0 {
		log.Printf("wg-quick:\n%s", out)
	}
	if err != nil {
		return fmt.Errorf("wg-quick up: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	deadline := time.Now().Add(12 * time.Second)
	for {
		if CheckInterfaceExistence(shareWgIface) {
			sm.vpnKind = "wireguard"
			sm.vpnFile = filepath.Base(src)
			if !wgHasHandshake() {
				log.Printf("share: %s is up, waiting for handshake before using it as uplink", shareWgIface)
			}
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("wireguard started but %s never appeared (%s)", shareWgIface, strings.TrimSpace(string(out)))
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (sm *ShareManager) startOpenVPNFile(src, user, pass string) error {
	bin := openvpnBin()
	if bin == "" {
		return fmt.Errorf("openvpn not found (apt-get install openvpn)")
	}
	cfgBytes, err := ioutil.ReadFile(src)
	if err != nil {
		return fmt.Errorf("openvpn config %s: %v", filepath.Base(src), err)
	}
	cfg := rewriteOvpnRemotes(string(cfgBytes))
	needs := ovpnNeedsAuth(cfg)
	authPath := ovpnAuthPath(src)
	if strings.TrimSpace(user) != "" && pass != "" {
		if err := writeOvpnAuthFile(authPath, user, pass); err != nil {
			return err
		}
	}
	if _, err := os.Stat(authPath); err != nil {
		authPath = ""
	}
	if needs && authPath == "" {
		return fmt.Errorf("this OpenVPN profile needs a username and password — enter them, Save, then Connect")
	}

	sm.stopOpenVPNLocked()
	rtDir := filepath.Join(shareVpnDir(), ".runtime")
	if err := os.MkdirAll(rtDir, 0700); err != nil {
		return err
	}
	rtCfg := filepath.Join(rtDir, "client.ovpn")
	body := cfg
	if authPath != "" {
		body = rewriteOvpnAuthLine(cfg, authPath)
	}
	if err := ioutil.WriteFile(rtCfg, []byte(body), 0600); err != nil {
		return err
	}
	_ = os.Remove(shareOvpnLogFile)

	args := []string{
		"--config", rtCfg,
		"--cd", filepath.Dir(src),
		"--daemon", "p4wnp1-ovpn",
		"--writepid", shareOvpnPidFile,
		"--log", shareOvpnLogFile,
		"--dev", shareOvpnIface,
		"--auth-nocache",
		"--connect-timeout", "15",
		"--pull-filter", "ignore", "route-ipv6",
		"--pull-filter", "ignore", "ifconfig-ipv6",
		"--pull-filter", "ignore", "tun-ipv6",
	}
	if authPath != "" {
		args = append(args, "--auth-user-pass", authPath)
	}
	cmd := exec.Command(bin, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	out, err := cmd.CombinedOutput()
	if len(out) > 0 {
		log.Printf("openvpn:\n%s", out)
	}
	if err != nil {
		return fmt.Errorf("openvpn: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	deadline := time.Now().Add(20 * time.Second)
	start := time.Now()
	for {
		if CheckInterfaceExistence(shareOvpnIface) {
			sm.vpnKind = "openvpn"
			sm.vpnFile = filepath.Base(src)
			sm.ovpnCmd = cmd
			return nil
		}
		tail := ovpnLogTail(1200)
		if msg := ovpnFailReason(tail); msg != "" {
			sm.stopOpenVPNLocked()
			return fmt.Errorf("openvpn: %s", msg)
		}
		if time.Since(start) > time.Second && !ovpnPidAlive() {
			sm.stopOpenVPNLocked()
			if tail != "" {
				return fmt.Errorf("openvpn exited: %s", tail)
			}
			return fmt.Errorf("openvpn exited before %s appeared", shareOvpnIface)
		}
		if time.Now().After(deadline) {
			sm.stopOpenVPNLocked()
			if tail != "" {
				return fmt.Errorf("openvpn started but %s never appeared: %s", shareOvpnIface, tail)
			}
			return fmt.Errorf("openvpn started but %s never appeared", shareOvpnIface)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func ovpnLogTail(n int) string {
	b, err := ioutil.ReadFile(shareOvpnLogFile)
	if err != nil {
		return ""
	}
	s := string(b)
	if len(s) > n {
		s = s[len(s)-n:]
	}
	return strings.TrimSpace(s)
}

func (sm *ShareManager) stopOpenVPNLocked() {
	if b, err := ioutil.ReadFile(shareOvpnPidFile); err == nil {
		pid := atoiSafe(strings.TrimSpace(string(b)))
		if pid > 1 {
			_ = syscall.Kill(pid, syscall.SIGTERM)
			time.Sleep(300 * time.Millisecond)
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
	_ = os.Remove(shareOvpnPidFile)
	_ = exec.Command("pkill", "-f", "p4wnp1-ovpn").Run()
	sm.ovpnCmd = nil
}

func (sm *ShareManager) StartVPN(kind, name, cfg, user, pass string) error {
	name = strings.TrimSpace(name)
	cfg = strings.TrimSpace(cfg)
	if kind == "" || kind == "auto" {
		if cfg != "" {
			kind = detectVpnKind(cfg)
		} else if strings.HasSuffix(strings.ToLower(name), ".ovpn") {
			kind = "openvpn"
		} else if name != "" {
			kind = "wireguard"
		}
	}
	if kind != "wireguard" && kind != "openvpn" {
		return fmt.Errorf("choose WireGuard or OpenVPN")
	}
	if err := sm.ensureVpnDir(); err != nil {
		return err
	}
	if name == "" {
		if cfg != "" {
			return fmt.Errorf("save the config into %s first, then select it", shareVpnDir())
		}
		return fmt.Errorf("select a saved VPN file in %s", shareVpnDir())
	}
	kind = inferVpnKind(kind, name)
	path, err := sm.vpnFilePath(kind, name)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("saved profile %s not found in %s", filepath.Base(path), shareVpnDir())
	}

	sm.mu.Lock()
	sm.vpnUserStop = false
	if sm.vpnStarting {
		sm.mu.Unlock()
		return fmt.Errorf("VPN connect already in progress")
	}
	sm.vpnStarting = true
	sm.mu.Unlock()
	defer func() {
		sm.mu.Lock()
		sm.vpnStarting = false
		sm.mu.Unlock()
	}()

	switch kind {
	case "wireguard":
		err = sm.startWireGuardFile(path)
	case "openvpn":
		err = sm.startOpenVPNFile(path, user, pass)
	default:
		return fmt.Errorf("choose WireGuard or OpenVPN")
	}
	if err != nil {
		return err
	}
	// Switch existing share to the tunnel only once it looks usable.
	// Boot starts wlan1 share first; a half-up VPN must not steal NAT.
	ready := true
	if kind == "wireguard" {
		ready = wgHasHandshake()
	} else {
		ready = ifaceIPv4(shareOvpnIface) != ""
	}
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.sharing && ready {
		up := sm.vpnIfaceLocked()
		if up != "" {
			sm.viaVPN = true
			_ = sm.applyNAT(up)
		}
	}
	return nil
}

func (sm *ShareManager) StopVPN() error {
	sm.mu.Lock()
	for sm.vpnStarting {
		sm.mu.Unlock()
		time.Sleep(200 * time.Millisecond)
		sm.mu.Lock()
	}
	defer sm.mu.Unlock()
	sm.vpnUserStop = true
	if wgQuickBin() != "" {
		_ = exec.Command(wgQuickBin(), "down", shareWgRuntimePath()).Run()
		_ = exec.Command(wgQuickBin(), "down", shareWgIface).Run()
	}
	sm.stopOpenVPNLocked()
	sm.vpnKind = ""
	sm.vpnFile = ""
	if sm.sharing {
		if alt := pickWifiUplink(); alt != "" {
			sm.viaVPN = false
			_ = sm.applyNAT(alt)
		} else {
			sm.flushNAT()
			sm.sharing = false
			sm.uplink = ""
		}
	}
	log.Printf("share: VPN stopped")
	return nil
}

func (sm *ShareManager) Status() shareStatus {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	st := shareStatus{
		Sharing:      sm.sharing,
		ViaVPN:       sm.viaVPN,
		Uplink:       sm.uplink,
		UplinkIP:     ifaceIPv4(sm.uplink),
		Wlan0Present: CheckInterfaceExistence("wlan0"),
		Wlan0IP:      ifaceIPv4("wlan0"),
		Wlan0IsAP:    wlan0IsAccessPoint(),
		Wlan1Present: CheckInterfaceExistence("wlan1"),
		Wlan1IP:      ifaceIPv4("wlan1"),
		UsbethIP:     ifaceIPv4("usbeth"),
		VpnRunning:   sm.vpnRunningLocked(),
		VpnKind:      sm.vpnKind,
		VpnFile:      sm.vpnFile,
		VpnIface:     sm.vpnIfaceLocked(),
		VpnDir:       shareVpnDir(),
		WgBin:        wgQuickBin(),
		OvpnBin:      openvpnBin(),
	}
	if st.VpnIface != "" {
		st.VpnIP = ifaceIPv4(st.VpnIface)
	}
	if len(sm.ListVPN("")) > 0 {
		st.HasConfig = true
	}
	if st.Wlan1Present {
		st.USBWifi = []string{"wlan1"}
	} else {
		st.USBWifi = listUSBWifiIfaces()
	}
	boot := sm.LoadBoot()
	st.BootVPN = boot.VPNEnable
	st.BootVPNKind = boot.VPNKind
	st.BootVPNName = boot.VPNName
	st.BootShare = boot.ShareEnable
	st.BootShareViaVPN = boot.ShareViaVPN
	st.DnsMode = boot.DNSMode
	st.Dns = boot.DNS
	if boot.DNSMode == "off" {
		st.DnsActive = "override off"
	} else if boot.DNSMode == "custom" {
		st.DnsActive = boot.DNS
	} else {
		st.DnsActive = shareDefaultDNS
	}
	wifiUp := pickWifiUplink()
	switch {
	case st.Sharing && st.ViaVPN:
		st.Note = "USB ethernet is sharing the VPN (over " + wifiUp + "). DNS: " + st.DnsActive + ". Renew DHCP on the PC if names do not resolve."
	case st.Sharing:
		st.Note = "USB ethernet is sharing " + st.Uplink + ". Gateway 172.16.0.1, DNS: " + st.DnsActive + ". Renew DHCP on the PC if names do not resolve."
	case wifiUp != "":
		st.Note = "WiFi uplink ready on " + wifiUp + " (" + ifaceIPv4(wifiUp) + "). Start share and/or VPN."
	case st.Wlan0IsAP:
		st.Note = "wlan0 is the AP (client failover). Plug in USB WiFi as wlan1 and Connect as client, or use Client mode on wlan0 from usbeth."
	default:
		st.Note = "Join WiFi as client: Client with Failover to AP on wlan0, or Connect as client on wlan1 (keeps the AP)."
	}
	return st
}

func (sm *ShareManager) StopQuiet() {
	_ = sm.StopShare()
	_ = sm.StopVPN()
}

func listUSBWifiIfaces() []string {
	var out []string
	for _, n := range netIfaceNames() {
		if n == "wlan0" || n == "lo" || !strings.HasPrefix(n, "wl") {
			continue
		}
		if _, err := os.Stat("/sys/class/net/" + n + "/wireless"); err == nil {
			out = append(out, n)
		}
	}
	return out
}

func writeJSONError(w http.ResponseWriter, code int, msg string) {
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (s *server) ServeShareAPI(w http.ResponseWriter, r *http.Request) {
	if s.rootSvc == nil || s.rootSvc.SubSysShare == nil {
		http.Error(w, `{"error":"share manager not ready"}`, http.StatusServiceUnavailable)
		return
	}
	sm := s.rootSvc.SubSysShare
	w.Header().Set("Content-Type", "application/json")
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case r.Method == http.MethodGet && (path == "/api/share/status" || path == "/api/share"):
		json.NewEncoder(w).Encode(sm.Status())
	case r.Method == http.MethodGet && path == "/api/share/boot":
		json.NewEncoder(w).Encode(sm.LoadBoot())
	case r.Method == http.MethodPost && path == "/api/share/boot":
		var cfg shareBootCfg
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := sm.SaveBoot(cfg); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		json.NewEncoder(w).Encode(sm.LoadBoot())
	case r.Method == http.MethodGet && path == "/api/share/vpn/list":
		kind := r.URL.Query().Get("kind")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"dir":   shareVpnDir(),
			"kind":  kind,
			"files": sm.ListVPN(kind),
		})
	case r.Method == http.MethodGet && path == "/api/share/vpn/config":
		kind := r.URL.Query().Get("kind")
		name := r.URL.Query().Get("name")
		cfg, err := sm.LoadVPN(kind, name)
		if err != nil {
			writeJSONError(w, http.StatusNotFound, err.Error())
			return
		}
		userName, hasAuth := sm.peekOvpnAuth(kind, name)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"kind":       kind,
			"name":       name,
			"config":     cfg,
			"dir":        shareVpnDir(),
			"needs_auth": ovpnNeedsAuth(cfg),
			"has_auth":   hasAuth,
			"username":   userName,
		})
	case r.Method == http.MethodPost && path == "/api/share/start":
		var req struct {
			ViaVPN bool `json:"via_vpn"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if err := sm.StartShare(req.ViaVPN); err != nil {
			writeJSONError(w, http.StatusConflict, err.Error())
			return
		}
		json.NewEncoder(w).Encode(sm.Status())
	case r.Method == http.MethodPost && path == "/api/share/stop":
		if err := sm.StopShare(); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		json.NewEncoder(w).Encode(sm.Status())
	case r.Method == http.MethodPost && path == "/api/share/vpn/save":
		var req struct {
			Kind     string `json:"kind"`
			Name     string `json:"name"`
			Config   string `json:"config"`
			Username string `json:"username"`
			Password string `json:"password"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		saved, err := sm.SaveVPN(req.Kind, req.Name, req.Config, req.Username, req.Password)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"name":  saved,
			"dir":   shareVpnDir(),
			"kind":  req.Kind,
			"files": sm.ListVPN(req.Kind),
		})
	case r.Method == http.MethodPost && path == "/api/share/vpn/delete":
		var req struct {
			Kind string `json:"kind"`
			Name string `json:"name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if err := sm.DeleteVPN(req.Kind, req.Name); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"dir":   shareVpnDir(),
			"files": sm.ListVPN(req.Kind),
		})
	case r.Method == http.MethodPost && path == "/api/share/vpn/start":
		var req struct {
			Kind     string `json:"kind"`
			Name     string `json:"name"`
			Config   string `json:"config"`
			Username string `json:"username"`
			Password string `json:"password"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if err := sm.StartVPN(req.Kind, req.Name, req.Config, req.Username, req.Password); err != nil {
			writeJSONError(w, http.StatusConflict, err.Error())
			return
		}
		st := sm.Status()
		st.Note = "VPN is up. USB ethernet traffic uses the tunnel if sharing is on."
		json.NewEncoder(w).Encode(st)
	case r.Method == http.MethodPost && path == "/api/share/vpn/stop":
		if err := sm.StopVPN(); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		json.NewEncoder(w).Encode(sm.Status())
	default:
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
	}
}
