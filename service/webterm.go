//go:build linux
// +build linux

package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
)

const webTermCwd = "/usr/share/S.o.D"

type WebTermManager struct {
	pty *PtySession
}

type webTermStatus struct {
	Running bool   `json:"running"`
	Cwd     string `json:"cwd"`
	Cols    int    `json:"cols"`
	Rows    int    `json:"rows"`
	Error   string `json:"error,omitempty"`
}

func NewWebTermManager() *WebTermManager {
	return &WebTermManager{pty: NewPtySession("webterm")}
}

func webTermEnv() []string {
	var env []string
	hasLang := false
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "TMUX=") || strings.HasPrefix(e, "TERM=") {
			continue
		}
		if strings.HasPrefix(e, "LANG=") {
			hasLang = true
		}
		env = append(env, e)
	}
	env = append(env, "TERM=xterm-256color", "COLORTERM=truecolor")
	if !hasLang {
		env = append(env, "LANG=C.UTF-8")
	}
	return env
}

func (wt *WebTermManager) Ensure() error {
	return wt.pty.Start(webTermCwd, webTermEnv(), []string{"/bin/bash", "-il"})
}

func (wt *WebTermManager) Status() webTermStatus {
	_ = wt.Ensure()
	cols, rows, running := wt.pty.Size()
	st := webTermStatus{Cwd: webTermCwd, Running: running, Cols: cols, Rows: rows}
	if !running {
		st.Error = "shell session is not running"
	}
	return st
}

func (wt *WebTermManager) Resize(cols, rows int) error {
	return wt.pty.Resize(cols, rows)
}

func (wt *WebTermManager) SendCtrlC() error {
	if err := wt.pty.Write([]byte{0x03}); err != nil {
		return fmt.Errorf("send Ctrl+C: %v", err)
	}
	return nil
}

func (wt *WebTermManager) SendInput(literal, named, ctrl string) error {
	return wt.pty.SendInput(literal, named, ctrl)
}

func (wt *WebTermManager) Stop() error {
	return wt.pty.Stop()
}

func (s *server) ServeWebTermAPI(w http.ResponseWriter, r *http.Request) {
	if s.rootSvc == nil || s.rootSvc.SubSysWebTerm == nil {
		http.Error(w, `{"error":"terminal not ready"}`, http.StatusServiceUnavailable)
		return
	}
	wt := s.rootSvc.SubSysWebTerm
	path := strings.TrimSuffix(r.URL.Path, "/")
	if path == "/api/term/ws" {
		_ = wt.Ensure()
		ServePtyWS(wt.pty, w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && (path == "/api/term/status" || path == "/api/term"):
		json.NewEncoder(w).Encode(wt.Status())
	case r.Method == http.MethodPost && path == "/api/term/keys":
		var req struct {
			Literal string `json:"literal"`
			Named   string `json:"named"`
			Ctrl    string `json:"ctrl"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if err := wt.SendInput(req.Literal, req.Named, req.Ctrl); err != nil {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	case r.Method == http.MethodPost && path == "/api/term/ctrlc":
		if err := wt.SendCtrlC(); err != nil {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(wt.Status())
	case r.Method == http.MethodPost && path == "/api/term/resize":
		var req struct {
			Cols int `json:"cols"`
			Rows int `json:"rows"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if err := wt.Resize(req.Cols, req.Rows); err != nil {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(wt.Status())
	default:
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
	}
}
