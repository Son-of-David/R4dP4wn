//go:build linux
// +build linux

package service

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)


type AirgeddonManager struct {
	root      *Service
	mu        sync.Mutex
	launchCmd *exec.Cmd
	pty       *PtySession
	attachTo  string
}

type airgeddonStatus struct {
	Running bool   `json:"running"`
	Bin     string `json:"airgeddon_bin"`
	Session string `json:"session,omitempty"`
	Monitor string `json:"monitor,omitempty"`
	Cols    int    `json:"cols"`
	Rows    int    `json:"rows"`
	Error   string `json:"error,omitempty"`
}

func NewAirgeddonManager(root *Service) *AirgeddonManager {
	return &AirgeddonManager{root: root, pty: NewPtySession("airgeddon")}
}

func airgeddonBin() string {
	for _, p := range []string{
		"/usr/bin/airgeddon",
		"/usr/sbin/airgeddon",
		"/usr/share/airgeddon/airgeddon.sh",
		"/usr/local/bin/airgeddon",
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath("airgeddon"); err == nil {
		return p
	}
	return ""
}

func tmuxBin() string {
	for _, p := range []string{"/usr/bin/tmux", "/bin/tmux", "/usr/local/bin/tmux"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath("tmux"); err == nil {
		return p
	}
	return "tmux"
}

func tmuxEnv() []string {
	var env []string
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "TMUX=") || strings.HasPrefix(e, "TERM=") {
			continue
		}
		env = append(env, e)
	}
	return append(env, "TERM=xterm-256color", "COLORTERM=truecolor", "TMUX_TMPDIR=/tmp", "AIRGEDDON_WINDOWS_HANDLING=tmux")
}

func runTmux(args ...string) (string, error) {
	cmd := exec.Command(tmuxBin(), args...)
	cmd.Env = tmuxEnv()
	cmd.Dir = "/"
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func airgeddonTmuxSessions() []string {
	out, err := runTmux("list-sessions", "-F", "#{session_name}")
	if err != nil || out == "" {
		return nil
	}
	var names []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "airgeddon") && !strings.HasPrefix(line, "r4d-") {
			names = append(names, line)
		}
	}
	return names
}

func (am *AirgeddonManager) sessionRunning() bool {
	if len(airgeddonTmuxSessions()) > 0 {
		return true
	}
	if exec.Command("pgrep", "-f", "airgeddon.sh").Run() == nil {
		return true
	}
	return false
}

func (am *AirgeddonManager) captureTarget() string {
	sessions := airgeddonTmuxSessions()
	if len(sessions) == 0 {
		return ""
	}
	return sessions[len(sessions)-1]
}

func (am *AirgeddonManager) EnsureAttach() error {
	sess := am.captureTarget()
	if sess == "" {
		return fmt.Errorf("airgeddon tmux session not found")
	}
	am.mu.Lock()
	same := am.pty.Running() && am.attachTo == sess
	am.mu.Unlock()
	if same {
		return nil
	}
	if am.pty.Running() {
		_ = am.pty.Stop()
	}
	if err := am.pty.Start("/", tmuxEnv(), []string{tmuxBin(), "-2", "attach-session", "-t", sess}); err != nil {
		return err
	}
	am.mu.Lock()
	am.attachTo = sess
	am.mu.Unlock()
	return nil
}

func wlan0MonitorName() string {
	if CheckInterfaceExistence("wlan0mon") {
		return "wlan0mon"
	}
	phy := wifiPhyName("wlan0")
	for _, n := range netIfaceNames() {
		if n == "wlan0" || n == "lo" {
			continue
		}
		if strings.HasSuffix(n, "mon") && (phy == "" || wifiPhyName(n) == phy) {
			return n
		}
		if phy != "" && wifiPhyName(n) == phy && ifaceIsMonitor(n) {
			return n
		}
	}
	if CheckInterfaceExistence("wlan0") && ifaceIsMonitor("wlan0") {
		return "wlan0"
	}
	return ""
}

func (am *AirgeddonManager) PrepareWlan0Monitor() (string, error) {
	if am.root != nil && am.root.SubSysWifi != nil {
		log.Printf("airgeddon: stopping hostapd/wpa so airmon-ng can take wlan0")
		_ = am.root.SubSysWifi.StopHostapd()
		_ = am.root.SubSysWifi.StopWpaSupplicant()
	}
	runQuiet("rfkill", "unblock", "all")
	runQuiet("nmcli", "device", "set", "wlan0", "managed", "no")
	runQuiet("nmcli", "device", "disconnect", "wlan0")
	bin := lookPathBin("airmon-ng")
	if bin == "" {
		return "", fmt.Errorf("airmon-ng not found (apt-get install aircrack-ng)")
	}
	out, err := exec.Command(bin, "check", "kill").CombinedOutput()
	if len(out) > 0 {
		log.Printf("airmon-ng check kill:\n%s", out)
	}
	if err != nil {
		log.Printf("airmon-ng check kill: %v", err)
	}
	out, err = exec.Command(bin, "start", "wlan0").CombinedOutput()
	if len(out) > 0 {
		log.Printf("airmon-ng start wlan0:\n%s", out)
	}
	if err != nil {
		return "", fmt.Errorf("airmon-ng start wlan0: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	deadline := time.Now().Add(8 * time.Second)
	for {
		if name := wlan0MonitorName(); name != "" {
			log.Printf("airgeddon: wlan0 is in monitor mode as %s", name)
			return name, nil
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("airmon-ng ran but no monitor interface appeared (%s)", strings.TrimSpace(string(out)))
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func (am *AirgeddonManager) Status() airgeddonStatus {
	st := airgeddonStatus{Bin: airgeddonBin(), Session: am.captureTarget(), Monitor: wlan0MonitorName()}
	st.Running = am.sessionRunning()
	if st.Running {
		_ = am.EnsureAttach()
	}
	cols, rows, _ := am.pty.Size()
	st.Cols, st.Rows = cols, rows
	return st
}

func (am *AirgeddonManager) Start() error {
	am.mu.Lock()
	already := am.sessionRunning()
	if already {
		am.mu.Unlock()
		return am.EnsureAttach()
	}
	bin := airgeddonBin()
	if bin == "" {
		am.mu.Unlock()
		return fmt.Errorf("airgeddon is not installed")
	}
	_, _ = runTmux("kill-session", "-t", "r4d-airgeddon")

	cmd := exec.Command("script", "-q", "-f", "-c", bin, "/tmp/r4d-airgeddon-launch.log")
	cmd.Env = tmuxEnv()
	cmd.Dir = "/usr/share/airgeddon"
	if _, err := os.Stat(cmd.Dir); err != nil {
		cmd.Dir = "/"
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		am.mu.Unlock()
		return fmt.Errorf("starting airgeddon: %v", err)
	}
	am.launchCmd = cmd
	go func() {
		_ = cmd.Wait()
		am.mu.Lock()
		if am.launchCmd == cmd {
			am.launchCmd = nil
		}
		am.mu.Unlock()
	}()
	am.mu.Unlock()

	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		if len(airgeddonTmuxSessions()) > 0 {
			log.Printf("airgeddon: tmux session %s", am.captureTarget())
			return am.EnsureAttach()
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("airgeddon started but its tmux session never appeared (it must create tmux itself; do not wrap it)")
}

func (am *AirgeddonManager) SendCtrlC() error {
	_ = am.EnsureAttach()
	return am.pty.Write([]byte{0x03})
}

func (am *AirgeddonManager) SendInput(literal, named, ctrl string) error {
	_ = am.EnsureAttach()
	return am.pty.SendInput(literal, named, ctrl)
}

func (am *AirgeddonManager) Stop() error {
	_ = am.pty.Stop()
	am.mu.Lock()
	am.attachTo = ""
	if am.launchCmd != nil && am.launchCmd.Process != nil {
		if pgid, err := syscall.Getpgid(am.launchCmd.Process.Pid); err == nil {
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		} else {
			_ = am.launchCmd.Process.Kill()
		}
		am.launchCmd = nil
	}
	am.mu.Unlock()
	for _, s := range airgeddonTmuxSessions() {
		_, _ = runTmux("kill-session", "-t", s)
	}
	_, _ = runTmux("kill-session", "-t", "r4d-airgeddon")
	_ = exec.Command("tmux", "kill-server").Run()
	_ = exec.Command("pkill", "-f", "airgeddon.sh").Run()
	_ = exec.Command("pkill", "-f", "/usr/bin/airgeddon").Run()
	_ = exec.Command("pkill", "-f", "/usr/share/airgeddon").Run()
	_ = exec.Command("pkill", "-x", "tmux").Run()
	_ = exec.Command("pkill", "tmux").Run()
	_ = exec.Command("pkill", "-x", "bash").Run()
	_ = exec.Command("pkill", "bash").Run()
	time.Sleep(300 * time.Millisecond)
	log.Printf("airgeddon: killed airgeddon, tmux, and bash")
	return nil
}

func (am *AirgeddonManager) RestoreWlan0() error {
	_ = am.Stop()
	log.Printf("airgeddon: restoring wlan0 (delete monitor VIFs, restart P4wnP1)")
	cleanupWifiMonitorVifs("wlan0")
	go func() {
		time.Sleep(800 * time.Millisecond)
		if err := exec.Command("systemctl", "restart", "P4wnP1").Run(); err != nil {
			log.Printf("airgeddon: systemctl restart P4wnP1: %v", err)
		}
	}()
	return nil
}

func (s *server) ServeAirgeddonAPI(w http.ResponseWriter, r *http.Request) {
	if s.rootSvc == nil || s.rootSvc.SubSysAirgeddon == nil {
		http.Error(w, `{"error":"airgeddon manager not ready"}`, http.StatusServiceUnavailable)
		return
	}
	am := s.rootSvc.SubSysAirgeddon
	path := strings.TrimSuffix(r.URL.Path, "/")
	if path == "/api/airgeddon/ws" {
		if am.sessionRunning() {
			_ = am.EnsureAttach()
		}
		ServePtyWS(am.pty, w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && (path == "/api/airgeddon/status" || path == "/api/airgeddon"):
		json.NewEncoder(w).Encode(am.Status())
	case r.Method == http.MethodPost && path == "/api/airgeddon/start":
		if err := am.Start(); err != nil {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(am.Status())
	case r.Method == http.MethodPost && path == "/api/airgeddon/stop":
		if err := am.Stop(); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(am.Status())
	case r.Method == http.MethodPost && path == "/api/airgeddon/keys":
		var req struct {
			Literal string `json:"literal"`
			Named   string `json:"named"`
			Ctrl    string `json:"ctrl"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if err := am.SendInput(req.Literal, req.Named, req.Ctrl); err != nil {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	case r.Method == http.MethodPost && path == "/api/airgeddon/ctrlc":
		if err := am.SendCtrlC(); err != nil {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(am.Status())
	case r.Method == http.MethodPost && path == "/api/airgeddon/monitor":
		name, err := am.PrepareWlan0Monitor()
		if err != nil {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		st := am.Status()
		json.NewEncoder(w).Encode(map[string]interface{}{
			"running":       st.Running,
			"airgeddon_bin": st.Bin,
			"session":       st.Session,
			"monitor":       name,
			"cols":          st.Cols,
			"rows":          st.Rows,
			"note":          "wlan0 is in monitor mode as " + name + ". Start Airgeddon and pick that interface. USB ethernet stays up; the onboard AP is down.",
		})
	case r.Method == http.MethodPost && path == "/api/airgeddon/restore":
		if err := am.RestoreWlan0(); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{
			"status": "restarting",
			"note":   "Deleted leftover monitor interfaces. P4wnP1 is restarting; USB ethernet will drop.",
		})
	default:
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
	}
}
