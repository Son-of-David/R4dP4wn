//go:build linux
// +build linux

package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	"github.com/mame82/P4wnP1_aloa/common"
)

const (
	captureMaxExtra  = 64
	captureMaxArgLen = 2048
	captureErrMax    = 8 * 1024
)

type CaptureManager struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	waitCh  chan error
	tool    string
	iface   string
	pcap    string
	started time.Time
	errText string
	errBuf  *cappedBuf
}

type captureStatus struct {
	Running    bool           `json:"running"`
	Tool       string         `json:"tool"`
	Iface      string         `json:"iface"`
	Pcap       string         `json:"pcap,omitempty"`
	PcapSize   int64          `json:"pcap_size"`
	StartedAgo string         `json:"started_ago,omitempty"`
	Error      string         `json:"error,omitempty"`
	TsharkBin  string         `json:"tshark_bin"`
	TcpdumpBin string         `json:"tcpdump_bin"`
	Ifaces     []captureIface `json:"ifaces"`
	Dir        string         `json:"dir"`
	Note       string         `json:"note,omitempty"`
}

type captureIface struct {
	Name     string `json:"name"`
	Up       bool   `json:"up"`
	Wireless bool   `json:"wireless"`
}

type captureFile struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	ModTime string `json:"mtime"`
}

type captureStartReq struct {
	Tool     string `json:"tool"`
	Iface    string `json:"iface"`
	BPF      string `json:"bpf"`
	Name     string `json:"name"`
	Extra    string `json:"extra"`
	Snaplen  int    `json:"snaplen"`
	Count    int    `json:"count"`
	Duration int    `json:"duration"`
	MaxMB    int    `json:"max_mb"`
	Promisc  *bool  `json:"promisc"`
	Monitor  bool   `json:"monitor"`
	Resolve  bool   `json:"resolve"`
}

type cappedBuf struct {
	mu  sync.Mutex
	b   bytes.Buffer
	max int
}

func (c *cappedBuf) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.max <= 0 {
		c.max = captureErrMax
	}
	c.b.Write(p)
	if c.b.Len() > c.max {
		keep := append([]byte(nil), c.b.Bytes()[c.b.Len()-c.max:]...)
		c.b.Reset()
		c.b.Write(keep)
	}
	return len(p), nil
}

func (c *cappedBuf) String() string {
	if c == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.TrimSpace(c.b.String())
}

func NewCaptureManager() *CaptureManager {
	return &CaptureManager{}
}

func captureDir() string {
	return filepath.Join(common.PATH_ROOT, "captures")
}

func tsharkBin() string  { return lookPathBin("tshark") }
func tcpdumpBin() string { return lookPathBin("tcpdump") }

func (cm *CaptureManager) ensureDir() error {
	return os.MkdirAll(captureDir(), 0700)
}

func sanitizeCaptureName(name string) (string, error) {
	name = strings.TrimSpace(name)
	name = filepath.Base(name)
	name = strings.ReplaceAll(name, " ", "_")
	lower := strings.ToLower(name)
	for _, ext := range []string{".pcapng", ".pcap", ".cap"} {
		if strings.HasSuffix(lower, ext) {
			name = name[:len(name)-len(ext)]
			lower = strings.ToLower(name)
			break
		}
	}
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		}
	}
	s := strings.Trim(b.String(), ".")
	if s == "" {
		s = "capture-" + time.Now().Format("20060102-150405")
	}
	return s + ".pcapng", nil
}

func captureFilePath(name string) (string, error) {
	safe, err := sanitizeCaptureName(name)
	if err != nil {
		return "", err
	}
	path := filepath.Join(captureDir(), safe)
	if filepath.Clean(filepath.Dir(path)) != filepath.Clean(captureDir()) {
		return "", fmt.Errorf("invalid capture file name")
	}
	return path, nil
}

func tokenizeArgs(s string) ([]string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if strings.ContainsRune(s, 0) {
		return nil, fmt.Errorf("invalid extra arguments")
	}
	var out []string
	var cur strings.Builder
	var quote rune
	esc := false
	for _, r := range s {
		if esc {
			cur.WriteRune(r)
			esc = false
			continue
		}
		if r == '\\' && quote != '\'' {
			esc = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
			continue
		}
		if r == '"' || r == '\'' {
			quote = r
			continue
		}
		if unicode.IsSpace(r) {
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
			continue
		}
		cur.WriteRune(r)
	}
	if quote != 0 {
		return nil, fmt.Errorf("unclosed quote in extra arguments")
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	if len(out) > captureMaxExtra {
		return nil, fmt.Errorf("too many extra arguments")
	}
	for _, a := range out {
		if len(a) > captureMaxArgLen {
			return nil, fmt.Errorf("extra argument too long")
		}
	}
	return out, nil
}

func listCaptureIfaces() []captureIface {
	var out []captureIface
	out = append(out, captureIface{Name: "any", Up: true})
	for _, n := range netIfaceNames() {
		if n == "lo" {
			out = append(out, captureIface{Name: n, Up: ifaceIPv4(n) != "" || CheckInterfaceExistence(n)})
			continue
		}
		wireless := false
		if _, err := os.Stat("/sys/class/net/" + n + "/wireless"); err == nil {
			wireless = true
		}
		up := false
		if b, err := ioutil.ReadFile("/sys/class/net/" + n + "/operstate"); err == nil {
			st := strings.TrimSpace(string(b))
			up = st == "up" || st == "unknown"
		}
		out = append(out, captureIface{Name: n, Up: up, Wireless: wireless})
	}
	return out
}

func validCaptureIface(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || name == "any" {
		return name == "any"
	}
	for _, r := range name {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' || r == ':'
		if !ok {
			return false
		}
	}
	return CheckInterfaceExistence(name)
}

func (cm *CaptureManager) Status() captureStatus {
	cm.mu.Lock()
	pcapPath := cm.pcap
	st := captureStatus{
		Running:    cm.cmd != nil && cm.cmd.Process != nil,
		Tool:       cm.tool,
		Iface:      cm.iface,
		TsharkBin:  tsharkBin(),
		TcpdumpBin: tcpdumpBin(),
		Ifaces:     listCaptureIfaces(),
		Dir:        captureDir(),
		Error:      cm.errText,
	}
	if pcapPath != "" {
		st.Pcap = filepath.Base(pcapPath)
	}
	if st.Running && !cm.started.IsZero() {
		st.StartedAgo = time.Since(cm.started).Truncate(time.Second).String()
	}
	if st.TsharkBin == "" && st.TcpdumpBin == "" {
		st.Note = "Install capture tools: apt-get install tshark tcpdump"
	} else if st.Running {
		st.Note = "Capturing to a pcap file. Stop flushes and saves it. Use Read on a saved file to decode."
	} else {
		st.Note = "Start writes a pcap under /usr/local/P4wnP1/captures/. Stop saves it. Monitor mode on wlan0 stops the AP."
	}
	cm.mu.Unlock()
	if pcapPath != "" {
		if fi, err := os.Stat(pcapPath); err == nil {
			st.PcapSize = fi.Size()
		}
	}
	return st
}

func (cm *CaptureManager) ListFiles() ([]captureFile, error) {
	if err := cm.ensureDir(); err != nil {
		return nil, err
	}
	ents, err := ioutil.ReadDir(captureDir())
	if err != nil {
		return nil, err
	}
	out := []captureFile{}
	for _, e := range ents {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		n := strings.ToLower(e.Name())
		if !strings.HasSuffix(n, ".pcap") && !strings.HasSuffix(n, ".pcapng") && !strings.HasSuffix(n, ".cap") {
			continue
		}
		out = append(out, captureFile{
			Name:    e.Name(),
			Size:    e.Size(),
			ModTime: e.ModTime().Format(time.RFC3339),
		})
	}
	return out, nil
}

func (cm *CaptureManager) DeleteFile(name string) error {
	path, err := captureFilePath(name)
	if err != nil {
		return err
	}
	cm.mu.Lock()
	active := cm.pcap == path && cm.cmd != nil
	cm.mu.Unlock()
	if active {
		return fmt.Errorf("cannot delete the capture that is currently running")
	}
	return os.Remove(path)
}

func (cm *CaptureManager) Stop() error {
	cm.mu.Lock()
	cmd := cm.cmd
	waitCh := cm.waitCh
	cm.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGINT)
	var err error
	if waitCh != nil {
		select {
		case err = <-waitCh:
		case <-time.After(2 * time.Second):
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			select {
			case err = <-waitCh:
			case <-time.After(2 * time.Second):
				err = fmt.Errorf("capture process did not exit")
			}
		}
	}
	cm.mu.Lock()
	if cm.cmd == cmd {
		cm.cmd = nil
		cm.waitCh = nil
	}
	cm.mu.Unlock()
	log.Printf("capture: stopped")
	return err
}

func (cm *CaptureManager) StopQuiet() {
	_ = cm.Stop()
}

func (cm *CaptureManager) Start(req captureStartReq) error {
	if err := cm.ensureDir(); err != nil {
		return err
	}
	tool := strings.ToLower(strings.TrimSpace(req.Tool))
	if tool == "" {
		tool = "tshark"
	}
	if tool != "tshark" && tool != "tcpdump" {
		return fmt.Errorf("tool must be tshark or tcpdump")
	}
	bin := tsharkBin()
	if tool == "tcpdump" {
		bin = tcpdumpBin()
	}
	if bin == "" {
		if tool == "tcpdump" {
			return fmt.Errorf("tcpdump not found (apt-get install tcpdump)")
		}
		return fmt.Errorf("tshark not found (apt-get install tshark)")
	}
	iface := strings.TrimSpace(req.Iface)
	if iface == "" {
		iface = "any"
	}
	if iface == "wlan1" {
		if err := ensureWlan1Present(); err != nil {
			return err
		}
	}
	if !validCaptureIface(iface) {
		return fmt.Errorf("interface %s is not present", iface)
	}
	extra, err := tokenizeArgs(req.Extra)
	if err != nil {
		return err
	}
	promisc := true
	if req.Promisc != nil {
		promisc = *req.Promisc
	}
	pcapPath, err := captureFilePath(req.Name)
	if err != nil {
		return err
	}
	bpf := strings.TrimSpace(strings.ReplaceAll(req.BPF, "\x00", ""))
	if strings.Contains(bpf, "\n") {
		return fmt.Errorf("filters cannot contain newlines")
	}

	if err := cm.Stop(); err != nil {
		return err
	}

	args := []string{}
	if tool == "tshark" {
		// Quiet write-only: no live decode, no -P / -Y / -V / -x.
		args = append(args, "-i", iface, "-q", "-w", pcapPath)
		if !req.Resolve {
			args = append(args, "-n")
		}
		if !promisc {
			args = append(args, "-p")
		}
		if req.Monitor {
			args = append(args, "-I")
		}
		if req.Snaplen > 0 {
			args = append(args, "-s", strconv.Itoa(req.Snaplen))
		}
		if req.Count > 0 {
			args = append(args, "-c", strconv.Itoa(req.Count))
		}
		if req.Duration > 0 {
			args = append(args, "-a", fmt.Sprintf("duration:%d", req.Duration))
		}
		maxMB := req.MaxMB
		if maxMB <= 0 {
			maxMB = 128
		}
		args = append(args, "-a", fmt.Sprintf("filesize:%d", maxMB*1024))
		if bpf != "" {
			args = append(args, "-f", bpf)
		}
	} else {
		args = append(args, "-i", iface, "-U", "-w", pcapPath)
		if !req.Resolve {
			args = append(args, "-nn")
		} else {
			args = append(args, "-n")
		}
		if !promisc {
			args = append(args, "-p")
		}
		if req.Monitor {
			args = append(args, "-I")
		}
		if req.Snaplen > 0 {
			args = append(args, "-s", strconv.Itoa(req.Snaplen))
		}
		if req.Count > 0 {
			args = append(args, "-c", strconv.Itoa(req.Count))
		}
		maxMB := req.MaxMB
		if maxMB > 0 {
			args = append(args, "-C", strconv.Itoa(maxMB))
		}
		if bpf != "" {
			if toks, err := tokenizeArgs(bpf); err == nil && len(toks) > 0 {
				args = append(args, toks...)
			} else {
				args = append(args, bpf)
			}
		}
	}
	args = append(args, extra...)

	errBuf := &cappedBuf{max: captureErrMax}
	cmd := exec.Command(bin, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdout = ioutil.Discard
	cmd.Stderr = errBuf
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %v", tool, err)
	}

	waitCh := make(chan error, 1)
	cm.mu.Lock()
	cm.cmd = cmd
	cm.waitCh = waitCh
	cm.tool = tool
	cm.iface = iface
	cm.pcap = pcapPath
	cm.started = time.Now()
	cm.errText = ""
	cm.errBuf = errBuf
	cm.mu.Unlock()

	go func() {
		err := cmd.Wait()
		waitCh <- err
		cm.mu.Lock()
		if cm.cmd == cmd {
			cm.cmd = nil
			cm.waitCh = nil
			if err != nil {
				msg := err.Error()
				if s := errBuf.String(); s != "" {
					msg = s
				}
				cm.errText = msg
			}
		}
		cm.mu.Unlock()
	}()
	if req.Duration > 0 && tool == "tcpdump" {
		go func(c *exec.Cmd, d int) {
			time.Sleep(time.Duration(d) * time.Second)
			cm.mu.Lock()
			same := cm.cmd == c
			cm.mu.Unlock()
			if same {
				_ = cm.Stop()
			}
		}(cmd, req.Duration)
	}
	log.Printf("capture: started %s on %s writing %s", tool, iface, filepath.Base(pcapPath))
	return nil
}

func (cm *CaptureManager) ReadPcap(name, display, extra string) (string, error) {
	bin := tsharkBin()
	if bin == "" {
		bin = tcpdumpBin()
	}
	if bin == "" {
		return "", fmt.Errorf("tshark or tcpdump required to read a pcap")
	}
	path, err := captureFilePath(name)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("pcap not found")
	}
	ex, err := tokenizeArgs(extra)
	if err != nil {
		return "", err
	}
	var args []string
	if filepath.Base(bin) == "tcpdump" || strings.HasSuffix(bin, "/tcpdump") {
		args = append(args, "-nn", "-r", path)
		if display != "" {
			args = append(args, display)
		}
	} else {
		args = append(args, "-n", "-r", path)
		if strings.TrimSpace(display) != "" {
			args = append(args, "-Y", display)
		}
	}
	args = append(args, ex...)
	cmd := exec.Command(bin, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	out, err := cmd.CombinedOutput()
	if len(out) > 512*1024 {
		out = out[:512*1024]
	}
	if err != nil && len(out) == 0 {
		return "", fmt.Errorf("%s: %v", filepath.Base(bin), err)
	}
	return string(out), nil
}

func (cm *CaptureManager) Help(tool string) string {
	bin := tsharkBin()
	if strings.ToLower(tool) == "tcpdump" {
		bin = tcpdumpBin()
	}
	if bin == "" {
		return "tool not installed"
	}
	cmd := exec.Command(bin, "-h")
	out, _ := cmd.CombinedOutput()
	s := string(out)
	if len(s) > 24*1024 {
		s = s[:24*1024]
	}
	return s
}

func (s *server) ServeCaptureAPI(w http.ResponseWriter, r *http.Request) {
	if s.rootSvc == nil || s.rootSvc.SubSysCapture == nil {
		http.Error(w, `{"error":"capture manager not ready"}`, http.StatusServiceUnavailable)
		return
	}
	cm := s.rootSvc.SubSysCapture
	path := strings.TrimSuffix(r.URL.Path, "/")
	if r.Method == http.MethodGet && strings.HasPrefix(path, "/api/capture/download/") {
		name := strings.TrimPrefix(path, "/api/capture/download/")
		p, err := captureFilePath(name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.ServeFile(w, r, p)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && (path == "/api/capture/status" || path == "/api/capture"):
		json.NewEncoder(w).Encode(cm.Status())
	case r.Method == http.MethodGet && path == "/api/capture/files":
		files, err := cm.ListFiles()
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"dir": captureDir(), "files": files})
	case r.Method == http.MethodGet && path == "/api/capture/help":
		json.NewEncoder(w).Encode(map[string]string{"tool": r.URL.Query().Get("tool"), "help": cm.Help(r.URL.Query().Get("tool"))})
	case r.Method == http.MethodPost && path == "/api/capture/start":
		var req captureStartReq
		_ = json.NewDecoder(r.Body).Decode(&req)
		if err := cm.Start(req); err != nil {
			writeJSONError(w, http.StatusConflict, err.Error())
			return
		}
		json.NewEncoder(w).Encode(cm.Status())
	case r.Method == http.MethodPost && path == "/api/capture/stop":
		_ = cm.Stop()
		json.NewEncoder(w).Encode(cm.Status())
	case r.Method == http.MethodPost && path == "/api/capture/read":
		var req struct {
			Name    string `json:"name"`
			Display string `json:"display_filter"`
			Extra   string `json:"extra"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		out, err := cm.ReadPcap(req.Name, req.Display, req.Extra)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"output": out})
	case r.Method == http.MethodPost && path == "/api/capture/delete":
		var req struct {
			Name string `json:"name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if err := cm.DeleteFile(req.Name); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		files, _ := cm.ListFiles()
		json.NewEncoder(w).Encode(map[string]interface{}{"files": files})
	default:
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
	}
}
