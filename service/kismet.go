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
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mame82/P4wnP1_aloa/common"
	pb "github.com/mame82/P4wnP1_aloa/proto"
)

const kismetHTTPPort = 2501

type KismetManager struct {
	root  *Service
	mu    sync.Mutex
	cmd   *exec.Cmd
	iface string
	done  chan struct{}
}

type kismetStatus struct {
	Running      bool     `json:"running"`
	Iface        string   `json:"iface"`
	ViaUSBEth    bool     `json:"via_usbeth"`
	ViaWiFiAP    bool     `json:"via_wifi"`
	Wlan0IsAP    bool     `json:"wlan0_is_ap"`
	Wlan1Present bool     `json:"wlan1_present"`
	USBWifi      []string `json:"usb_wifi"`
	UIPort       int      `json:"ui_port"`
	Error        string   `json:"error,omitempty"`
	KismetBin    string   `json:"kismet_bin"`
}

func NewKismetManager(root *Service) *KismetManager {
	return &KismetManager{root: root}
}

func kismetBin() string {
	for _, p := range []string{"/usr/bin/kismet", "/usr/sbin/kismet", "/usr/local/bin/kismet"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath("kismet"); err == nil {
		return p
	}
	return ""
}

func wirelessIfaces() []string {
	var out []string
	ents, err := ioutil.ReadDir("/sys/class/net")
	if err != nil {
		return out
	}
	for _, e := range ents {
		name := e.Name()
		if _, err := os.Stat("/sys/class/net/" + name + "/wireless"); err == nil {
			out = append(out, name)
		}
	}
	return out
}

func requestHostIP(r *http.Request) string {
	h := r.Host
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	return h
}

func (km *KismetManager) runningLocked() bool {
	return km.cmd != nil && km.cmd.Process != nil
}

func (km *KismetManager) Status(r *http.Request) kismetStatus {
	km.mu.Lock()
	defer km.mu.Unlock()
	st := kismetStatus{
		UIPort:    kismetHTTPPort,
		KismetBin: kismetBin(),
	}
	st.Running = km.runningLocked()
	st.Iface = km.iface
	host := requestHostIP(r)
	st.ViaUSBEth = host == "172.16.0.1" || strings.HasPrefix(host, "172.16.0.")
	st.ViaWiFiAP = host == "172.24.0.1" || strings.HasPrefix(host, "172.24.0.")
	if km.root != nil && km.root.SubSysWifi != nil && km.root.SubSysWifi.State != nil {
		st.Wlan0IsAP = km.root.SubSysWifi.State.Mode == pb.WiFiStateMode_AP_UP
	}
	for _, n := range wirelessIfaces() {
		if n == "wlan0" {
			continue
		}
		st.USBWifi = append(st.USBWifi, n)
		if n == "wlan1" {
			st.Wlan1Present = true
		}
	}
	return st
}

func killKismetProcesses() {
	runQuiet("pkill", "-x", "kismet")
	runQuiet("pkill", "-x", "kismet_server")
	runQuiet("pkill", "-f", "kismet_cap_")
}

func (km *KismetManager) storedWifiSettingsForAP() *pb.WiFiSettings {
	if km.root == nil || km.root.SubSysDataStore == nil {
		return nil
	}
	name := &pb.StringMessage{}
	if err := km.root.SubSysDataStore.Get(cSTORE_STARTUP_MASTER_TEMPLATE, name); err == nil && name.Msg != "" {
		mt := &pb.MasterTemplate{}
		if err := km.root.SubSysDataStore.Get(cSTORE_PREFIX_MASTER_TEMPLATE+name.Msg, mt); err == nil && mt.TemplateNameWifi != "" {
			ws := &pb.WiFiSettings{}
			if err := km.root.SubSysDataStore.Get(cSTORE_PREFIX_WIFI_SETTINGS+mt.TemplateNameWifi, ws); err == nil {
				log.Printf("Kismet: restoring WiFi from stored template %q", mt.TemplateNameWifi)
				return ws
			}
		}
	}
	if km.root.SubSysWifi != nil && km.root.SubSysWifi.State != nil {
		return km.root.SubSysWifi.State.CurrentSettings
	}
	return nil
}

func (km *KismetManager) restoreOnboardAP() error {
	if km.root == nil || km.root.SubSysWifi == nil {
		return fmt.Errorf("wifi subsystem not ready")
	}
	// AIC8800 needs a settle after wlan0mon is deleted; a full service restart
	// happens to provide that delay. Bringing wlan0 up alone does not start hostapd.
	time.Sleep(2 * time.Second)

	settings := km.storedWifiSettingsForAP()
	if settings == nil || settings.Disabled {
		return fmt.Errorf("no stored WiFi AP settings to restore")
	}
	if settings.WorkingMode != pb.WiFiWorkingMode_AP && settings.WorkingMode != pb.WiFiWorkingMode_STA_FAILOVER_AP {
		return fmt.Errorf("stored WiFi settings are not AP mode")
	}

	if km.root.SubSysWifi.State != nil {
		km.root.SubSysWifi.State.Mode = pb.WiFiStateMode_STA_NOT_CONNECTED
	}

	log.Printf("Kismet: redeploying onboard WiFi AP (same path as P4wnP1 start, USB left alone)")
	var err error
	if km.root.SubSysRPC != nil {
		_, err = km.root.SubSysRPC.DeployWiFiSettings(context.Background(), settings)
	} else {
		_, err = km.root.SubSysWifi.DeploySettings(settings)
	}
	if err != nil {
		log.Printf("Kismet: AP restore attempt 1 failed: %v; retrying", err)
		time.Sleep(2 * time.Second)
		if km.root.SubSysRPC != nil {
			_, err = km.root.SubSysRPC.DeployWiFiSettings(context.Background(), settings)
		} else {
			_, err = km.root.SubSysWifi.DeploySettings(settings)
		}
	}
	if err != nil {
		log.Printf("Kismet: failed to restore AP: %v", err)
		return err
	}
	if km.root.SubSysWifi.State == nil || km.root.SubSysWifi.State.Mode != pb.WiFiStateMode_AP_UP {
		log.Printf("Kismet: hostapd returned but AP not up yet; retrying deploy")
		time.Sleep(2 * time.Second)
		if km.root.SubSysRPC != nil {
			_, err = km.root.SubSysRPC.DeployWiFiSettings(context.Background(), settings)
		} else {
			_, err = km.root.SubSysWifi.DeploySettings(settings)
		}
		if err != nil {
			return err
		}
	}
	log.Printf("Kismet: onboard WiFi AP restored")
	return nil
}

// Stop kills kismet, removes leftover monitor interfaces, and brings the
// onboard AP back if kismet had taken wlan0.
func (km *KismetManager) Stop() error {
	return km.stop(true)
}

// StopQuiet kills kismet without restarting hostapd (service shutdown).
func (km *KismetManager) StopQuiet() error {
	return km.stop(false)
}

func (km *KismetManager) stop(restoreAP bool) error {
	km.mu.Lock()
	iface := km.iface
	err := km.stopLocked()
	km.mu.Unlock()

	killKismetProcesses()
	time.Sleep(400 * time.Millisecond)

	switch iface {
	case "wlan1":
		cleanupWifiMonitorVifs("wlan1")
	case "wlan0":
		cleanupWifiMonitorVifs("wlan0")
		if restoreAP {
			if rerr := km.restoreOnboardAP(); rerr != nil && err == nil {
				err = rerr
			}
		}
	default:
		// Process already gone (or never tracked) but wlan0mon may still own the radio.
		if hasMonitorVifOnPhy("wlan0") {
			cleanupWifiMonitorVifs("wlan0")
			if restoreAP {
				if rerr := km.restoreOnboardAP(); rerr != nil && err == nil {
					err = rerr
				}
			}
		}
	}
	return err
}

func (km *KismetManager) stopLocked() error {
	if km.cmd != nil && km.cmd.Process != nil {
		pgid, err := syscall.Getpgid(km.cmd.Process.Pid)
		if err == nil {
			_ = syscall.Kill(-pgid, syscall.SIGTERM)
		} else {
			_ = km.cmd.Process.Signal(syscall.SIGTERM)
		}
		done := km.done
		if done != nil {
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				if pgid, err := syscall.Getpgid(km.cmd.Process.Pid); err == nil {
					_ = syscall.Kill(-pgid, syscall.SIGKILL)
				} else {
					_ = km.cmd.Process.Kill()
				}
				<-done
			}
		}
	}
	killKismetProcesses()
	km.cmd = nil
	km.iface = ""
	km.done = nil
	return nil
}

func runRenameUSBWifi() (string, error) {
	script := filepath.Join(common.PATH_BASH_SCRIPTS, "rename-usb-wifi.sh")
	if _, err := os.Stat(script); err != nil {
		return "", fmt.Errorf("rename script missing at %s (copy dist/scripts/rename-usb-wifi.sh)", script)
	}
	cmd := exec.Command("/bin/bash", script)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func prepareIface(name string) {
	_ = exec.Command("rfkill", "unblock", "all").Run()
	_ = exec.Command("nmcli", "device", "set", name, "managed", "no").Run()
	_ = exec.Command("ip", "link", "set", name, "up").Run()
	time.Sleep(400 * time.Millisecond)
}

func (km *KismetManager) Start(source string) error {
	km.mu.Lock()
	defer km.mu.Unlock()
	if km.runningLocked() {
		return fmt.Errorf("kismet already running on %s", km.iface)
	}
	bin := kismetBin()
	if bin == "" {
		return fmt.Errorf("kismet is not installed (apt-get install kismet)")
	}

	iface := source
	switch source {
	case "wlan0":
		if km.root != nil && km.root.SubSysWifi != nil {
			log.Printf("Kismet: stopping hostapd so wlan0 can enter monitor mode")
			_ = km.root.SubSysWifi.StopHostapd()
			_ = km.root.SubSysWifi.StopWpaSupplicant()
			if km.root.SubSysWifi.State != nil {
				km.root.SubSysWifi.State.Mode = pb.WiFiStateMode_STA_NOT_CONNECTED
			}
		}
		prepareIface("wlan0")
		iface = "wlan0"
	case "wlan1":
		out, err := runRenameUSBWifi()
		if len(out) > 0 {
			log.Printf("Kismet rename-usb-wifi:\n%s", out)
		}
		if err != nil {
			return fmt.Errorf("USB WiFi rename failed: %v (%s)", err, strings.TrimSpace(out))
		}
		deadline := time.Now().Add(8 * time.Second)
		for {
			if CheckInterfaceExistence("wlan1") {
				break
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("wlan1 not found. Plug in a USB WiFi adapter, then try again")
			}
			time.Sleep(300 * time.Millisecond)
		}
		prepareIface("wlan1")
		iface = "wlan1"
	default:
		return fmt.Errorf("unknown kismet source %q", source)
	}

	logFile, err := os.OpenFile("/tmp/r4d-kismet.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	cmd := exec.Command(bin, "-c", iface)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return fmt.Errorf("starting kismet: %v", err)
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		logFile.Close()
		close(done)
		km.mu.Lock()
		if km.cmd == cmd {
			km.cmd = nil
			km.iface = ""
			km.done = nil
		}
		km.mu.Unlock()
		log.Printf("kismet process exited")
	}()
	km.cmd = cmd
	km.done = done
	km.iface = iface
	log.Printf("Kismet started on %s (pid %d), UI :%d", iface, cmd.Process.Pid, kismetHTTPPort)
	return nil
}

func (s *server) ServeKismetAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if s.rootSvc == nil || s.rootSvc.SubSysKismet == nil {
		http.Error(w, `{"error":"kismet manager not ready"}`, http.StatusServiceUnavailable)
		return
	}
	km := s.rootSvc.SubSysKismet
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case r.Method == http.MethodGet && (path == "/api/kismet/status" || path == "/api/kismet"):
		json.NewEncoder(w).Encode(km.Status(r))
	case r.Method == http.MethodPost && path == "/api/kismet/start":
		var req struct {
			Source string `json:"source"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Source == "" {
			http.Error(w, `{"error":"source required (wlan0 or wlan1)"}`, http.StatusBadRequest)
			return
		}
		if err := km.Start(req.Source); err != nil {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(km.Status(r))
	case r.Method == http.MethodPost && path == "/api/kismet/stop":
		if err := km.Stop(); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(km.Status(r))
	default:
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
	}
}
