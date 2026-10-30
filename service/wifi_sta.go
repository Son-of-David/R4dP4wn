//go:build linux
// +build linux

package service

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mame82/P4wnP1_aloa/common"
	pb "github.com/mame82/P4wnP1_aloa/proto"
)

func lookPathBin(names ...string) string {
	for _, n := range names {
		for _, dir := range []string{"/sbin/", "/usr/sbin/", "/usr/bin/", "/bin/"} {
			p := dir + n
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	return ""
}

func wpaSupplicantBin() string { return lookPathBin("wpa_supplicant") }
func wpaPassphraseBin() string { return lookPathBin("wpa_passphrase") }
func dhcpClientCmd(ifName string) []string {
	if p := lookPathBin("dhcpcd"); p != "" {
		return []string{p, "-b", "-C", "wpa_supplicant", ifName}
	}
	if p := lookPathBin("dhclient"); p != "" {
		return []string{p, "-4", "-nw", ifName}
	}
	if p := lookPathBin("udhcpc"); p != "" {
		return []string{p, "-i", ifName, "-b"}
	}
	if p := lookPathBin("busybox"); p != "" {
		return []string{p, "udhcpc", "-i", ifName, "-b"}
	}
	return nil
}

func (wSvc *WiFiService) staIfaceName() string {
	if wSvc != nil && strings.TrimSpace(wSvc.StaIface) != "" {
		return wSvc.StaIface
	}
	if wSvc != nil && wSvc.IfaceName != "" {
		return wSvc.IfaceName
	}
	return wifi_if_name
}

func ifaceIPv4(ifName string) string {
	ifi, err := net.InterfaceByName(ifName)
	if err != nil {
		return ""
	}
	addrs, err := ifi.Addrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok || ipn.IP == nil || ipn.IP.To4() == nil || ipn.IP.IsLoopback() {
			continue
		}
		return ipn.IP.String()
	}
	return ""
}

func prepareWifiInterfaceForSTA(ifName string) {
	cleanupWifiMonitorVifs(ifName)
	runQuiet("rfkill", "unblock", "all")
	runQuiet("nmcli", "device", "set", ifName, "managed", "no")
	runQuiet("nmcli", "device", "disconnect", ifName)
	runQuiet("systemctl", "stop", "wpa_supplicant@"+ifName+".service")
	runQuiet("wpa_cli", "-i", ifName, "terminate")
	runQuiet("pkill", "-f", "wpa_supplicant.*-i "+ifName)
	runQuiet("pkill", "-f", "wpa_supplicant.*"+ifName)
	if CheckInterfaceExistence(ifName) {
		runQuiet("ip", "link", "set", ifName, "down")
		runQuiet(iwBin(), "dev", ifName, "set", "type", "managed")
		runQuiet("ip", "link", "set", ifName, "up")
	}
	_ = waitUntilIwReady(ifName, 8*time.Second)
}

func startDhcpClientRaw(ifName string) error {
	if argv := dhcpClientCmd(ifName); len(argv) > 0 {
		cmd := exec.Command(argv[0], argv[1:]...)
		out, err := cmd.CombinedOutput()
		if len(out) > 0 {
			log.Printf("dhcp %s: %s", ifName, strings.TrimSpace(string(out)))
		}
		if err == nil {
			return nil
		}
		log.Printf("dhcp %s helper failed (%v), using built-in client", ifName, err)
	} else {
		log.Printf("dhcp %s: no dhcpcd/dhclient/udhcpc, using built-in client", ifName)
	}
	return startDhcp4Builtin(ifName)
}

func stopDhcpClientRaw(ifName string) {
	stopDhcp4Builtin(ifName)
	if p := lookPathBin("dhcpcd"); p != "" {
		runQuiet(p, "-x", ifName)
	}
	runQuiet("dhclient", "-r", ifName)
	runQuiet("pkill", "-f", "dhclient.*"+ifName)
	runQuiet("pkill", "-f", "udhcpc.*"+ifName)
	runQuiet("pkill", "-f", "busybox udhcpc.*"+ifName)
}

func applyStaDhcp(root *Service, ifName string) error {
	return startDhcpClientRaw(ifName)
}


func iptablesBin() string { return lookPathBin("iptables") }

func iptablesCmd(args ...string) error {
	bin := iptablesBin()
	if bin == "" {
		return fmt.Errorf("iptables not found")
	}
	cmd := exec.Command(bin, args...)
	out, err := cmd.CombinedOutput()
	if err != nil && len(out) > 0 {
		log.Printf("iptables %v: %s", args, strings.TrimSpace(string(out)))
	}
	return err
}

func iptablesEnsureChain(table, chain string) {
	args := []string{}
	if table != "" && table != "filter" {
		args = append(args, "-t", table)
	}
	if iptablesCmd(append(args, "-N", chain)...) != nil {
		_ = iptablesCmd(append(args, "-F", chain)...)
	} else {
		_ = iptablesCmd(append(args, "-F", chain)...)
	}
}

func iptablesEnsureJump(table, hook, chain string) {
	args := []string{}
	if table != "" && table != "filter" {
		args = append(args, "-t", table)
	}
	if iptablesCmd(append(append([]string{}, args...), "-C", hook, "-j", chain)...) != nil {
		_ = iptablesCmd(append(append([]string{}, args...), "-I", hook, "1", "-j", chain)...)
	}
}

func waitIfaceIPv4(ifName string, d time.Duration) string {
	deadline := time.Now().Add(d)
	for {
		if ip := ifaceIPv4(ifName); ip != "" {
			return ip
		}
		if time.Now().After(deadline) {
			return ""
		}
		time.Sleep(400 * time.Millisecond)
	}
}

func ensureApDhcpGateway(root *Service) {
	if root == nil || root.SubSysNetwork == nil {
		return
	}
	nim, err := root.SubSysNetwork.GetManagedInterface(wifi_if_name)
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
	gw := s.IpAddress4
	if gw == "" {
		gw = "172.24.0.1"
	}
	changed := false
	if s.DhcpServerSettings.Options[3] == "" {
		s.DhcpServerSettings.Options[3] = gw
		changed = true
	}
	if s.DhcpServerSettings.Options[6] == "" {
		s.DhcpServerSettings.Options[6] = "8.8.8.8,1.1.1.1"
		changed = true
	}
	if changed {
		log.Printf("WiFi: advertising DHCP router %s on %s so AP clients can use the wlan1 uplink", gw, wifi_if_name)
		if err := nim.DeploySettings(s); err != nil {
			log.Printf("WiFi: DHCP gateway update: %v", err)
		}
	}
}

func shareStaUplink(uplink string) {
	if uplink == "" {
		return
	}
	runQuiet("sysctl", "-w", "net.ipv4.ip_forward=1")
	runQuiet("sysctl", "-w", "net.ipv4.conf.all.rp_filter=2")
	runQuiet("sysctl", "-w", "net.ipv4.conf."+uplink+".rp_filter=2")
	iptablesEnsureChain("nat", "P4WNP1STA")
	iptablesEnsureChain("filter", "P4WNP1STA")
	iptablesEnsureJump("nat", "POSTROUTING", "P4WNP1STA")
	iptablesEnsureJump("filter", "FORWARD", "P4WNP1STA")
	_ = iptablesCmd("-t", "nat", "-A", "P4WNP1STA", "-o", uplink, "-j", "MASQUERADE")
	// LAN side is always usbeth. Include the onboard AP only when the uplink
	// is a different iface (wlan1); never NAT an interface to itself.
	lans := []string{"usbeth"}
	if uplink != "wlan0" && uplink != wifi_if_name {
		runQuiet("sysctl", "-w", "net.ipv4.conf.wlan0.rp_filter=2")
		lans = append(lans, "wlan0")
	}
	for _, lan := range lans {
		_ = iptablesCmd("-A", "P4WNP1STA", "-i", lan, "-o", uplink, "-j", "ACCEPT")
		_ = iptablesCmd("-A", "P4WNP1STA", "-i", uplink, "-o", lan, "-m", "state", "--state", "RELATED,ESTABLISHED", "-j", "ACCEPT")
	}
	log.Printf("WiFi: sharing %s uplink to %v", uplink, lans)
}

// pickWifiUplink returns the best WiFi client iface with an IPv4 address.
// Prefer USB wlan1 (AP can stay up). Fall back to onboard wlan0 when it is
// in STA mode (not hosting the AP).
func pickWifiUplink() string {
	if ifaceIPv4("wlan1") != "" {
		return "wlan1"
	}
	if ifaceIPv4("wlan0") != "" && !wlan0IsAccessPoint() {
		return "wlan0"
	}
	return ""
}

func wlan0IsAccessPoint() bool {
	// hostapd pid file or process is the most reliable signal.
	if _, err := os.Stat("/run/hostapd.pid"); err == nil {
		return true
	}
	if out, err := exec.Command("pgrep", "-x", "hostapd").CombinedOutput(); err == nil && len(strings.TrimSpace(string(out))) > 0 {
		return true
	}
	return false
}

func unshareStaUplink() {
	_ = iptablesCmd("-t", "nat", "-F", "P4WNP1STA")
	_ = iptablesCmd("-F", "P4WNP1STA")
}

func restoreOnboardAP(root *Service) {
	if root == nil || root.SubSysWifi == nil {
		return
	}
	ws := (*pb.WiFiSettings)(nil)
	if root.SubSysDataStore != nil {
		name := &pb.StringMessage{}
		if err := root.SubSysDataStore.Get(cSTORE_STARTUP_MASTER_TEMPLATE, name); err == nil && name.Msg != "" {
			stored := &pb.WiFiSettings{}
			key := cSTORE_PREFIX_WIFI_SETTINGS + name.Msg
			if err := root.SubSysDataStore.Get(key, stored); err == nil && stored != nil {
				ws = stored
			}
		}
		if ws == nil {
			stored := &pb.WiFiSettings{}
			if err := root.SubSysDataStore.Get(cSTORE_PREFIX_WIFI_SETTINGS+"startup", stored); err == nil {
				ws = stored
			}
		}
	}
	if ws == nil && root.SubSysWifi.State != nil && root.SubSysWifi.State.CurrentSettings != nil {
		ws = root.SubSysWifi.State.CurrentSettings
	}
	if ws == nil {
		return
	}
	ws.WorkingMode = pb.WiFiWorkingMode_AP
	ws.Disabled = false
	if _, err := root.SubSysWifi.DeploySettings(ws); err != nil {
		log.Printf("WiFi: restore onboard AP: %v", err)
	}
}

func ensureWlan1Present() error {
	if CheckInterfaceExistence("wlan1") {
		return nil
	}
	out, err := runRenameUSBWifi()
	if len(out) > 0 {
		log.Printf("WiFi rename-usb-wifi:\n%s", out)
	}
	if err != nil {
		return fmt.Errorf("USB WiFi rename failed: %v (%s)", err, strings.TrimSpace(out))
	}
	deadline := time.Now().Add(8 * time.Second)
	for {
		if CheckInterfaceExistence("wlan1") {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("wlan1 not found. Plug in a USB WiFi adapter, then try again")
		}
		time.Sleep(300 * time.Millisecond)
	}
}


func wifiClientSSID(ws *pb.WiFiSettings) string {
	if ws == nil || len(ws.Client_BSSList) == 0 || ws.Client_BSSList[0] == nil {
		return ""
	}
	return strings.TrimSpace(ws.Client_BSSList[0].SSID)
}

func wifiClientPSK(ws *pb.WiFiSettings) string {
	if ws == nil || len(ws.Client_BSSList) == 0 || ws.Client_BSSList[0] == nil {
		return ""
	}
	return ws.Client_BSSList[0].PSK
}

func wlan1StaPath() string {
	return filepath.Join(common.PATH_ROOT, "wifi", "wlan1-sta.json")
}

type wlan1StaCfg struct {
	SSID string `json:"ssid"`
	PSK  string `json:"psk"`
}

func saveWlan1Sta(ssid, psk string) {
	ssid = strings.TrimSpace(ssid)
	if ssid == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(wlan1StaPath()), 0700); err != nil {
		return
	}
	b, err := json.Marshal(wlan1StaCfg{SSID: ssid, PSK: psk})
	if err != nil {
		return
	}
	_ = ioutil.WriteFile(wlan1StaPath(), b, 0600)
}

func loadWlan1Sta() (ssid, psk string, ok bool) {
	b, err := ioutil.ReadFile(wlan1StaPath())
	if err != nil {
		return "", "", false
	}
	var cfg wlan1StaCfg
	if json.Unmarshal(b, &cfg) != nil {
		return "", "", false
	}
	ssid = strings.TrimSpace(cfg.SSID)
	return ssid, cfg.PSK, ssid != ""
}

func (wSvc *WiFiService) joinWlan1Later(ssid, psk string) {
	ssid = strings.TrimSpace(ssid)
	if ssid == "" {
		return
	}
	log.Printf("WiFi: after AP, joining %q on wlan1", ssid)
	time.Sleep(2 * time.Second)
	deadline := time.Now().Add(90 * time.Second)
	for {
		if err := ensureWlan1Present(); err == nil {
			break
		}
		if time.Now().After(deadline) {
			log.Printf("WiFi: wlan1 not present for stored client SSID %q", ssid)
			return
		}
		time.Sleep(2 * time.Second)
	}
	if ifaceIPv4("wlan1") != "" && wSvc != nil && wSvc.StaIface == "wlan1" {
		log.Printf("WiFi: wlan1 already has an address, skipping join of %q", ssid)
		return
	}
	if err := wSvc.JoinAccessPoint("wlan1", ssid, psk); err != nil {
		log.Printf("WiFi: wlan1 client join %q failed: %v", ssid, err)
	}
}

func (s *Service) StartBootNetworking() {
	if s == nil {
		return
	}
	log.Printf("boot: waiting for WiFi client uplink (wlan0 STA or wlan1), then share, then VPN")
	// Give DeploySettings time to finish STA_FAILOVER_AP (wlan0 STA try, or AP+wlan1).
	time.Sleep(12 * time.Second)
	deadline := time.Now().Add(100 * time.Second)
	joinedWlan1 := false
	for time.Now().Before(deadline) {
		if up := pickWifiUplink(); up != "" {
			log.Printf("boot: WiFi uplink ready on %s (%s)", up, ifaceIPv4(up))
			break
		}
		joining := s.SubSysWifi != nil && s.SubSysWifi.CmdWpaSupplicant != nil
		// If onboard is AP (failover path), try USB client so Share/VPN still work.
		if !joining && !joinedWlan1 && wlan0IsAccessPoint() {
			ssid, psk, ok := loadWlan1Sta()
			if !ok && s.SubSysWifi != nil && s.SubSysWifi.State != nil {
				ssid = wifiClientSSID(s.SubSysWifi.State.CurrentSettings)
				psk = wifiClientPSK(s.SubSysWifi.State.CurrentSettings)
				ok = ssid != ""
			}
			if ok && ssid != "" && s.SubSysWifi != nil {
				log.Printf("boot: AP is up on wlan0; joining %q on wlan1 for uplink", ssid)
				joinedWlan1 = true
				if err := s.SubSysWifi.JoinAccessPoint("wlan1", ssid, psk); err != nil {
					log.Printf("boot: wlan1 join %q: %v", ssid, err)
					joinedWlan1 = false
				}
			}
		}
		time.Sleep(3 * time.Second)
	}
	up := pickWifiUplink()
	if up == "" {
		log.Printf("boot: no WiFi client uplink (wlan0 STA or wlan1), skipping VPN/share autostart")
		return
	}
	log.Printf("boot: using WiFi uplink %s (%s)", up, ifaceIPv4(up))
	if !waitUplinkInternet(up, 20*time.Second) {
		log.Printf("boot: %s has an address but no default route yet", up)
	}
	if s.SubSysShare != nil {
		s.SubSysShare.ApplyBootConfig()
	}
}

func (wSvc *WiFiService) JoinAccessPoint(iface, ssid, psk string) error {
	iface = strings.TrimSpace(iface)
	ssid = strings.TrimSpace(ssid)
	if iface != "wlan0" && iface != "wlan1" {
		return fmt.Errorf("interface must be wlan0 or wlan1")
	}
	if ssid == "" {
		return fmt.Errorf("SSID is required")
	}
	if iface == "wlan1" {
		if err := ensureWlan1Present(); err != nil {
			return err
		}
	} else if wSvc != nil {
		_ = wSvc.StopHostapd()
	}

	_ = wSvc.StopWpaSupplicant()
	stopDhcpClientRaw(iface)
	wSvc.StaIface = iface
	wSvc.PathWpaSupplicantConf = fmt.Sprintf("/tmp/wpa_supplicant_%s.conf", iface)
	prepareWifiInterfaceForSTA(iface)

	cfg := []*pb.WiFiBSSCfg{{SSID: ssid, PSK: psk}}
	confstr, err := wifiCreateWpaSupplicantConfStringList(cfg)
	if err != nil {
		return err
	}
	if err = os.WriteFile(wSvc.PathWpaSupplicantConf, []byte(confstr), 0600); err != nil {
		return err
	}
	if err = wSvc.StartWpaSupplicant(WPA_SUPPLICANT_CONNECT_TIMEOUT); err != nil {
		return err
	}
	if err = applyStaDhcp(wSvc.RootSvc, iface); err != nil {
		log.Printf("WiFi: connected to %q on %s but DHCP failed: %v", ssid, iface, err)
		return fmt.Errorf("associated to %s but DHCP failed: %v", ssid, err)
	}
	ip := waitIfaceIPv4(iface, 12*time.Second)
	if wSvc.State != nil && iface != "wlan1" {
		wSvc.State.Mode = pb.WiFiStateMode_STA_CONNECTED
		wSvc.State.Ssid = ssid
		if wSvc.State.CurrentSettings == nil {
			wSvc.State.CurrentSettings = &pb.WiFiSettings{}
		}
		wSvc.State.CurrentSettings.WorkingMode = pb.WiFiWorkingMode_STA
		wSvc.State.CurrentSettings.Client_BSSList = cfg
	}
	bootShare := false
	if wSvc.RootSvc != nil && wSvc.RootSvc.SubSysShare != nil {
		bootShare = wSvc.RootSvc.SubSysShare.LoadBoot().ShareEnable
	}
	if iface == "wlan1" {
		ensureApDhcpGateway(wSvc.RootSvc)
		saveWlan1Sta(ssid, psk)
	}
	if !bootShare {
		shareStaUplink(iface)
	} else if wSvc.RootSvc != nil && wSvc.RootSvc.SubSysShare != nil {
		// Share manager owns NAT when boot share is enabled.
		_ = wSvc.RootSvc.SubSysShare.StartShare(false)
	}
	log.Printf("WiFi: joined %q on %s (ip %s)", ssid, iface, ip)
	return nil
}

func (wSvc *WiFiService) LeaveAccessPoint(iface string) error {
	iface = strings.TrimSpace(iface)
	if iface == "" {
		iface = wSvc.staIfaceName()
	}
	_ = wSvc.StopWpaSupplicant()
	stopDhcpClientRaw(iface)
	unshareStaUplink()
	wSvc.StaIface = ""
	if wSvc.State != nil && (iface == "wlan0" || iface == wifi_if_name) {
		wSvc.State.Mode = pb.WiFiStateMode_STA_NOT_CONNECTED
		wSvc.State.Ssid = ""
	}
	if iface == "wlan0" || iface == wifi_if_name {
		restoreOnboardAP(wSvc.RootSvc)
	}
	return nil
}

type wifiStaStatus struct {
	Wlan0Present bool     `json:"wlan0_present"`
	Wlan1Present bool     `json:"wlan1_present"`
	USBWifi      []string `json:"usb_wifi"`
	Iface        string   `json:"iface"`
	SSID         string   `json:"ssid"`
	Running      bool     `json:"running"`
	IP           string   `json:"ip"`
	Note         string   `json:"note,omitempty"`
	Error        string   `json:"error,omitempty"`
}

func (wSvc *WiFiService) StaStatus() wifiStaStatus {
	st := wifiStaStatus{}
	for _, n := range wirelessIfaces() {
		if n == "wlan0" {
			st.Wlan0Present = true
			continue
		}
		st.USBWifi = append(st.USBWifi, n)
		if n == "wlan1" {
			st.Wlan1Present = true
		}
	}
	st.Iface = wSvc.staIfaceName()
	st.Running = wSvc.CmdWpaSupplicant != nil && wSvc.CmdWpaSupplicant.Process != nil
	if wSvc.State != nil {
		st.SSID = wSvc.State.Ssid
		if wSvc.State.Mode == pb.WiFiStateMode_STA_CONNECTED {
			st.Running = true
		}
	}
	if st.Running {
		st.IP = ifaceIPv4(st.Iface)
	}
	if st.Running && st.Iface == "wlan1" {
		st.Note = "wlan0 AP and usbeth clients can use this uplink."
	} else if !st.Wlan1Present {
		st.Note = "wlan1 appears after a USB WiFi adapter is plugged in."
	}
	return st
}

func (s *server) ServeWifiStaAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if s.rootSvc == nil || s.rootSvc.SubSysWifi == nil {
		http.Error(w, `{"error":"wifi not ready"}`, http.StatusServiceUnavailable)
		return
	}
	ws := s.rootSvc.SubSysWifi
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case r.Method == http.MethodGet && (path == "/api/wifi/status" || path == "/api/wifi"):
		json.NewEncoder(w).Encode(ws.StaStatus())
	case r.Method == http.MethodPost && path == "/api/wifi/join":
		var req struct {
			Iface string `json:"iface"`
			SSID  string `json:"ssid"`
			PSK   string `json:"psk"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if err := ws.JoinAccessPoint(req.Iface, req.SSID, req.PSK); err != nil {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(ws.StaStatus())
	case r.Method == http.MethodPost && path == "/api/wifi/disconnect":
		var req struct {
			Iface string `json:"iface"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if err := ws.LeaveAccessPoint(req.Iface); err != nil {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(ws.StaStatus())
	default:
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
	}
}
