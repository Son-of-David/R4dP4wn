//go:build linux
// +build linux

package service

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	pb "github.com/mame82/P4wnP1_aloa/proto"
)

const (
	nmUnmanagedDropIn = "/etc/NetworkManager/conf.d/99-p4wnp1-unmanaged.conf"
	wifiIfaceWait     = 45 * time.Second
)

func waitForNetworkInterface(name string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if CheckInterfaceExistence(name) {
			return true
		}
		if timeout > 0 && time.Now().After(deadline) {
			return false
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func runQuiet(name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Stdout = ioutil.Discard
	cmd.Stderr = ioutil.Discard
	_ = cmd.Run()
}

func hostapdBin() string {
	if _, err := os.Stat("/usr/sbin/hostapd"); err == nil {
		return "/usr/sbin/hostapd"
	}
	if p, err := exec.LookPath("hostapd"); err == nil {
		return p
	}
	return "/usr/sbin/hostapd"
}

func iwBin() string {
	for _, p := range []string{"/sbin/iw", "/usr/sbin/iw", "/usr/bin/iw"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath("iw"); err == nil {
		return p
	}
	return "iw"
}

func writeNetworkManagerUnmanaged(ifName string) {
	dir := filepath.Dir(nmUnmanagedDropIn)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}
	body := fmt.Sprintf("[keyfile]\nunmanaged-devices=interface-name:%s\n", ifName)
	prev, _ := ioutil.ReadFile(nmUnmanagedDropIn)
	if string(prev) == body {
		runQuiet("nmcli", "device", "set", ifName, "managed", "no")
		return
	}
	if err := ioutil.WriteFile(nmUnmanagedDropIn, []byte(body), 0644); err != nil {
		log.Printf("could not write %s: %v", nmUnmanagedDropIn, err)
		return
	}
	runQuiet("nmcli", "general", "reload")
	runQuiet("nmcli", "device", "set", ifName, "managed", "no")
}

func iwDevReady(ifName string) bool {
	cmd := exec.Command(iwBin(), "dev", ifName, "info")
	cmd.Stdout = ioutil.Discard
	cmd.Stderr = ioutil.Discard
	return cmd.Run() == nil
}

func waitUntilIwReady(ifName string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if CheckInterfaceExistence(ifName) && iwDevReady(ifName) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func wifiDriverModule(ifName string) string {
	p := fmt.Sprintf("/sys/class/net/%s/device/driver/module", ifName)
	target, err := os.Readlink(p)
	if err != nil {
		return ""
	}
	mod := filepath.Base(target)
	if mod == "" || mod == "." {
		return ""
	}
	return mod
}

func reloadWifiDriver(ifName string) {
	mod := wifiDriverModule(ifName)
	if mod == "" {
		log.Printf("WiFi: no kernel module found for %s; skipping reload", ifName)
		return
	}
	log.Printf("WiFi: reloading module %s to recover nl80211 on %s", mod, ifName)
	runQuiet("ip", "link", "set", ifName, "down")
	time.Sleep(300 * time.Millisecond)
	cmd := exec.Command("modprobe", "-r", mod)
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Printf("WiFi: modprobe -r %s: %v (%s)", mod, err, strings.TrimSpace(string(out)))
	}
	time.Sleep(2 * time.Second)
	cmd = exec.Command("modprobe", mod)
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Printf("WiFi: modprobe %s: %v (%s)", mod, err, strings.TrimSpace(string(out)))
	}
	if !waitForNetworkInterface(ifName, 15*time.Second) {
		log.Printf("WiFi: %s did not return after reloading %s", ifName, mod)
	}
}

func stopWifiStationHelpers(ifName string) {
	runQuiet("rfkill", "unblock", "all")
	runQuiet("rfkill", "unblock", "wifi")
	runQuiet("rfkill", "unblock", "wlan")
	runQuiet("nmcli", "device", "set", ifName, "managed", "no")
	runQuiet("nmcli", "device", "disconnect", ifName)
	runQuiet("systemctl", "stop", "wpa_supplicant@"+ifName+".service")
	runQuiet("wpa_cli", "-i", ifName, "terminate")
	runQuiet("pkill", "-f", "wpa_supplicant.*"+ifName)
	if ifName == wifi_if_name || ifName == "wlan0" {
		runQuiet("pkill", "-x", "hostapd")
	}
}

func netIfaceNames() []string {
	ents, err := ioutil.ReadDir("/sys/class/net")
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(ents))
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out
}

func wifiPhyName(ifName string) string {
	target, err := os.Readlink("/sys/class/net/" + ifName + "/phy80211")
	if err != nil {
		return ""
	}
	return filepath.Base(target)
}

func ifaceIsMonitor(ifName string) bool {
	if strings.HasSuffix(ifName, "mon") {
		return true
	}
	out, err := exec.Command(iwBin(), "dev", ifName, "info").CombinedOutput()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "type monitor")
}

func hasMonitorVifOnPhy(ifName string) bool {
	if ifaceIsMonitor(ifName) {
		return true
	}
	phy := wifiPhyName(ifName)
	want := ifName + "mon"
	for _, n := range netIfaceNames() {
		if n == ifName {
			continue
		}
		if n == want {
			return true
		}
		if phy != "" && wifiPhyName(n) == phy && ifaceIsMonitor(n) {
			return true
		}
	}
	return false
}

// cleanupWifiMonitorVifs removes Kismet/airmon virtual monitor interfaces on the
// same phy as ifName, then puts ifName back in managed mode. hostapd cannot start
// while wlan0mon (or similar) still owns the radio.
func cleanupWifiMonitorVifs(ifName string) {
	phy := wifiPhyName(ifName)
	for _, n := range netIfaceNames() {
		if n == ifName || n == "lo" {
			continue
		}
		samePhy := phy != "" && wifiPhyName(n) == phy
		relatedMon := n == ifName+"mon" || (strings.HasSuffix(n, "mon") && samePhy)
		if !relatedMon && !(samePhy && ifaceIsMonitor(n)) {
			continue
		}
		log.Printf("WiFi: deleting leftover monitor interface %s (phy %s)", n, phy)
		runQuiet("ip", "link", "set", n, "down")
		runQuiet(iwBin(), "dev", n, "del")
	}
	if CheckInterfaceExistence(ifName) {
		runQuiet("ip", "link", "set", ifName, "down")
		runQuiet(iwBin(), "dev", ifName, "set", "type", "managed")
		runQuiet("ip", "link", "set", ifName, "up")
	}
}

func prepareWifiInterfaceForAP(ifName string) {
	prepareWifiInterfaceForAPLevel(ifName, "soft")
}

func prepareWifiInterfaceForAPLevel(ifName, level string) {
	log.Printf("Preparing WiFi interface '%s' for AP mode (%s) ...", ifName, level)
	writeNetworkManagerUnmanaged(ifName)
	stopWifiStationHelpers(ifName)
	cleanupWifiMonitorVifs(ifName)

	switch level {
	case "reload":
		reloadWifiDriver(ifName)
	case "hard":
		runQuiet("ip", "link", "set", ifName, "down")
		time.Sleep(time.Second)
	default:
		// soft: do not bounce the AIC8800 firmware unless we have to
	}

	if CheckInterfaceExistence(ifName) {
		runQuiet(iwBin(), "dev", ifName, "set", "type", "managed")
		runQuiet("ip", "link", "set", ifName, "up")
	}
	if !waitUntilIwReady(ifName, 8*time.Second) {
		log.Printf("WiFi: %s not ready for nl80211 yet", ifName)
		runQuiet("ip", "link", "set", ifName, "up")
		_ = waitUntilIwReady(ifName, 4*time.Second)
	}
}

func wifiCreateHostapdConfStringMode(ws *pb.WiFiSettings, useHT bool) (config string, err error) {
	if ws.WorkingMode != pb.WiFiWorkingMode_AP && ws.WorkingMode != pb.WiFiWorkingMode_STA_FAILOVER_AP {
		return "", fmt.Errorf("Couldn't create hostapd configuration, the settings don't include an AP")
	}
	if ws.Ap_BSS == nil {
		return "", fmt.Errorf("WiFiSettings don't contain a BSS configuration for an AP")
	}

	reg := strings.ToUpper(strings.TrimSpace(ws.Regulatory))
	if len(reg) != 2 {
		reg = "US"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "interface=%s\n", wifi_if_name)
	b.WriteString("driver=nl80211\n")
	b.WriteString("hw_mode=g\n")
	fmt.Fprintf(&b, "country_code=%s\n", reg)
	b.WriteString("ieee80211d=0\n")
	b.WriteString("ieee80211h=0\n")
	fmt.Fprintf(&b, "ssid=%s\n", ws.Ap_BSS.SSID)
	fmt.Fprintf(&b, "channel=%d\n", ws.Channel)
	b.WriteString("macaddr_acl=0\n")
	if ws.HideSsid {
		b.WriteString("ignore_broadcast_ssid=2\n")
	} else {
		b.WriteString("ignore_broadcast_ssid=0\n")
	}

	if useHT {
		b.WriteString("ieee80211n=1\n")
		b.WriteString("wmm_enabled=1\n")
		b.WriteString("ht_capab=[SHORT-GI-20]\n")
	} else {
		b.WriteString("ieee80211n=0\n")
		b.WriteString("wmm_enabled=0\n")
	}

	if ws.AuthMode == pb.WiFiAuthMode_WPA2_PSK {
		b.WriteString("auth_algs=1\n")
		b.WriteString("wpa=2\n")
		b.WriteString("wpa_key_mgmt=WPA-PSK\n")
		fmt.Fprintf(&b, "wpa_passphrase=%s\n", ws.Ap_BSS.PSK)
		b.WriteString("rsn_pairwise=CCMP\n")
	} else {
		b.WriteString("auth_algs=3\n")
	}

	return b.String(), nil
}

func hostapdCreateConfigFileMode(s *pb.WiFiSettings, filename string, useHT bool) error {
	log.Printf("Creating hostapd configuration file at '%s' (802.11n=%v)\n", filename, useHT)
	fileContent, err := wifiCreateHostapdConfStringMode(s, useHT)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(filename, []byte(fileContent), 0644)
}
