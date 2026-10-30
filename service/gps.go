//go:build linux
// +build linux

package service

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	gpsPipePort     = 9999
	gpsMonLogPath   = "/tmp/r4d-cgps.log"
	gpsMonMaxOutput = 128 * 1024
)

var gpsAnsiRe = regexp.MustCompile(`\x1b\[[0-9;?=]*[A-Za-z]|\x1b\].*?(?:\x07|\x1b\\)|\x1b[()].`)

type GpsManager struct {
	mu      sync.Mutex
	viewCmd *exec.Cmd
	viewDone chan struct{}
	pipeLn  net.Listener
}

type gpsStatus struct {
	Running  bool   `json:"running"`
	Viewing  bool   `json:"viewing"`
	PipeHost string `json:"pipe_host"`
	PipePort int    `json:"pipe_port"`
	PipeNote string `json:"pipe_note"`
	Output   string `json:"output,omitempty"`
	Error    string `json:"error,omitempty"`
}

func NewGpsManager() *GpsManager {
	return &GpsManager{}
}

func runLogged(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	msg := strings.TrimSpace(string(out))
	if msg != "" {
		log.Printf("gps: %s %s: %s", name, strings.Join(args, " "), msg)
	} else {
		log.Printf("gps: %s %s", name, strings.Join(args, " "))
	}
	return err
}

func gpsdActive() bool {
	for _, u := range []string{"GPSD.service", "GPSD", "gpsd.service", "gpsd", "gpsd.socket"} {
		if exec.Command("systemctl", "is-active", "--quiet", u).Run() == nil {
			return true
		}
	}
	return false
}

func (gm *GpsManager) viewingLocked() bool {
	if gm.viewCmd == nil || gm.viewCmd.Process == nil {
		return false
	}
	if err := gm.viewCmd.Process.Signal(syscall.Signal(0)); err != nil {
		gm.viewCmd = nil
		gm.viewDone = nil
		return false
	}
	return true
}

func stripGpsMonOutput(raw []byte) string {
	s := string(raw)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = gpsAnsiRe.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\x00", "")
	if len(s) > gpsMonMaxOutput {
		s = s[len(s)-gpsMonMaxOutput:]
		if i := strings.IndexByte(s, '\n'); i >= 0 && i < 200 {
			s = s[i+1:]
		}
	}
	return s
}

func (gm *GpsManager) readViewOutput() string {
	raw, err := ioutil.ReadFile(gpsMonLogPath)
	if err != nil {
		return ""
	}
	return stripGpsMonOutput(raw)
}

func (gm *GpsManager) Status(r *http.Request) gpsStatus {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	host := requestHostIP(r)
	st := gpsStatus{
		Running:  gpsdActive(),
		Viewing:  gm.viewingLocked(),
		PipeHost: host,
		PipePort: gpsPipePort,
		PipeNote: fmt.Sprintf("GPS data should be piped to %s:%d", host, gpsPipePort),
	}
	if st.Viewing {
		st.Output = gm.readViewOutput()
	}
	if gm.pipeLn == nil && st.Running {
		st.Error = "gpsd is up but :" + fmt.Sprintf("%d", gpsPipePort) + " is not listening yet"
	}
	return st
}

func (gm *GpsManager) startPipe9999() error {
	gm.stopPipeLocked()
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", gpsPipePort))
	if err != nil {
		return fmt.Errorf("listen :%d: %v", gpsPipePort, err)
	}
	gm.pipeLn = ln
	log.Printf("gps: piping gpsd to 0.0.0.0:%d", gpsPipePort)
	go func(l net.Listener) {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go proxyGpsd(conn)
		}
	}(ln)
	return nil
}

func proxyGpsd(client net.Conn) {
	defer client.Close()
	backend, err := net.DialTimeout("tcp", "127.0.0.1:2947", 3*time.Second)
	if err != nil {
		fmt.Fprintf(client, "gpsd not reachable on 127.0.0.1:2947: %v\n", err)
		return
	}
	defer backend.Close()
	go io.Copy(backend, client)
	io.Copy(client, backend)
}

func (gm *GpsManager) stopPipeLocked() {
	if gm.pipeLn != nil {
		_ = gm.pipeLn.Close()
		gm.pipeLn = nil
	}
}

func (gm *GpsManager) hideLocked() {
	if gm.viewCmd != nil && gm.viewCmd.Process != nil {
		if pgid, err := syscall.Getpgid(gm.viewCmd.Process.Pid); err == nil {
			_ = syscall.Kill(-pgid, syscall.SIGTERM)
			time.Sleep(200 * time.Millisecond)
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		} else {
			_ = gm.viewCmd.Process.Kill()
		}
		if gm.viewDone != nil {
			select {
			case <-gm.viewDone:
			case <-time.After(2 * time.Second):
			}
		}
	}
	gm.viewCmd = nil
	gm.viewDone = nil
	_ = exec.Command("pkill", "-x", "cgps").Run()
	_ = exec.Command("pkill", "-x", "gpsmon").Run()
}

func (gm *GpsManager) Start() error {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	for _, args := range [][]string{
		{"systemctl", "enable", "GPSD.service"},
		{"systemctl", "start", "GPSD.service"},
		{"systemctl", "enable", "gpsd.service"},
		{"systemctl", "start", "gpsd.service"},
		{"systemctl", "restart", "gpsd.socket"},
		{"systemctl", "restart", "gpsd"},
		{"systemctl", "restart", "GPSD"},
	} {
		_ = runLogged(args[0], args[1:]...)
	}
	time.Sleep(400 * time.Millisecond)
	if err := gm.startPipe9999(); err != nil {
		log.Printf("gps: pipe :%d: %v", gpsPipePort, err)
		return err
	}
	return nil
}

func (gm *GpsManager) Stop() error {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	gm.hideLocked()
	gm.stopPipeLocked()
	for _, args := range [][]string{
		{"systemctl", "stop", "GPSD"},
		{"systemctl", "disable", "GPSD"},
		{"systemctl", "stop", "gpsd"},
		{"systemctl", "disable", "gpsd"},
		{"systemctl", "stop", "gpsd.socket"},
		{"systemctl", "disable", "gpsd.socket"},
	} {
		_ = runLogged(args[0], args[1:]...)
	}
	return nil
}

func (gm *GpsManager) Hide() {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	gm.hideLocked()
}

// Shutdown kills cgps and the :9999 pipe; leaves gpsd as the user left it.
func (gm *GpsManager) Shutdown() {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	gm.hideLocked()
	gm.stopPipeLocked()
}

func (gm *GpsManager) View() error {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	if gm.viewingLocked() {
		return nil
	}
	for _, args := range [][]string{
		{"systemctl", "restart", "gpsd.socket"},
		{"systemctl", "restart", "gpsd"},
		{"systemctl", "restart", "GPSD"},
	} {
		_ = runLogged(args[0], args[1:]...)
	}
	time.Sleep(400 * time.Millisecond)
	if gm.pipeLn == nil {
		_ = gm.startPipe9999()
	}
	_ = os.Remove(gpsMonLogPath)
	// cgps is a curses UI; script gives it a TTY so output can be tailed for the web view.
	cmd := exec.Command("script", "-q", "-f", "-c", "cgps", gpsMonLogPath)
	cmd.Env = append(os.Environ(), "TERM=xterm", "COLUMNS=80", "LINES=24")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting cgps: %v", err)
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
		gm.mu.Lock()
		if gm.viewCmd == cmd {
			gm.viewCmd = nil
			gm.viewDone = nil
		}
		gm.mu.Unlock()
	}()
	gm.viewCmd = cmd
	gm.viewDone = done
	log.Printf("gps: cgps started (pid %d)", cmd.Process.Pid)
	return nil
}

func (s *server) ServeGpsAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if s.rootSvc == nil || s.rootSvc.SubSysGps == nil {
		http.Error(w, `{"error":"gps manager not ready"}`, http.StatusServiceUnavailable)
		return
	}
	gm := s.rootSvc.SubSysGps
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case r.Method == http.MethodGet && (path == "/api/gps/status" || path == "/api/gps"):
		json.NewEncoder(w).Encode(gm.Status(r))
	case r.Method == http.MethodPost && path == "/api/gps/start":
		if err := gm.Start(); err != nil {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(gm.Status(r))
	case r.Method == http.MethodPost && path == "/api/gps/stop":
		if err := gm.Stop(); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(gm.Status(r))
	case r.Method == http.MethodPost && path == "/api/gps/view":
		if err := gm.View(); err != nil {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		time.Sleep(300 * time.Millisecond)
		json.NewEncoder(w).Encode(gm.Status(r))
	case r.Method == http.MethodPost && path == "/api/gps/hide":
		gm.Hide()
		json.NewEncoder(w).Encode(gm.Status(r))
	default:
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
	}
}
