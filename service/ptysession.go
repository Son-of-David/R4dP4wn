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
	"unsafe"

	"github.com/gorilla/websocket"
	"golang.org/x/sys/unix"
)

const (
	ptyMaxOut = 256 * 1024
	ptyCols   = 120
	ptyRows   = 40
)

var termWSUpgrader = websocket.Upgrader{
	CheckOrigin:     func(*http.Request) bool { return true },
	ReadBufferSize:  1024,
	WriteBufferSize: 8192,
}

type ptyEvent struct {
	reset bool
	data  []byte
}

type PtySession struct {
	name   string
	mu     sync.Mutex
	cmd    *exec.Cmd
	master *os.File
	buf    []byte
	cols   int
	rows   int
	subs   map[chan ptyEvent]struct{}
}

func NewPtySession(name string) *PtySession {
	return &PtySession{
		name: name,
		cols: ptyCols,
		rows: ptyRows,
		subs: make(map[chan ptyEvent]struct{}),
	}
}

func clampTermSize(cols, rows int) (int, int) {
	if cols < 20 {
		cols = 20
	}
	if cols > 300 {
		cols = 300
	}
	if rows < 8 {
		rows = 8
	}
	if rows > 120 {
		rows = 120
	}
	return cols, rows
}

func openPty(cols, rows int) (master, slave *os.File, err error) {
	master, err = os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}
	if err = unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		master.Close()
		return nil, nil, fmt.Errorf("unlockpt: %v", err)
	}
	var n uint32
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), uintptr(unix.TIOCGPTN), uintptr(unsafe.Pointer(&n)))
	if errno != 0 {
		master.Close()
		return nil, nil, fmt.Errorf("tiocgptn: %v", errno)
	}
	slave, err = os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		return nil, nil, err
	}
	ws := unix.Winsize{Row: uint16(rows), Col: uint16(cols)}
	_ = unix.IoctlSetWinsize(int(master.Fd()), unix.TIOCSWINSZ, &ws)
	return master, slave, nil
}

func indexByteFrom(b []byte, c byte, start int) int {
	if start < 0 {
		start = 0
	}
	for i := start; i < len(b); i++ {
		if b[i] == c {
			return i
		}
	}
	return -1
}

var ptyNamedKeys = map[string]string{
	"Enter":      "\r",
	"Return":     "\r",
	"Backspace":  "\x7f",
	"Tab":        "\t",
	"Escape":     "\x1b",
	"ArrowUp":    "\x1b[A",
	"ArrowDown":  "\x1b[B",
	"ArrowRight": "\x1b[C",
	"ArrowLeft":  "\x1b[D",
	"Delete":     "\x1b[3~",
	"Home":       "\x1b[H",
	"End":        "\x1b[F",
	"PageUp":     "\x1b[5~",
	"PageDown":   "\x1b[6~",
	"Space":      " ",
}

func encodePtyInput(literal, named, ctrl string) ([]byte, error) {
	if named != "" {
		seq, ok := ptyNamedKeys[named]
		if !ok {
			return nil, fmt.Errorf("unsupported key %q", named)
		}
		return []byte(seq), nil
	}
	if ctrl != "" {
		ch := strings.ToLower(strings.TrimSpace(ctrl))
		if len(ch) != 1 || ch[0] < 'a' || ch[0] > 'z' {
			return nil, fmt.Errorf("unsupported ctrl key")
		}
		return []byte{ch[0] - 'a' + 1}, nil
	}
	if literal != "" {
		if len(literal) > 256 {
			literal = literal[:256]
		}
		return []byte(literal), nil
	}
	return nil, fmt.Errorf("no keys")
}

func (p *PtySession) sizeLocked() (int, int) {
	cols, rows := p.cols, p.rows
	if cols <= 0 {
		cols = ptyCols
	}
	if rows <= 0 {
		rows = ptyRows
	}
	return cols, rows
}

func (p *PtySession) runningLocked() bool {
	if p.cmd == nil || p.cmd.Process == nil || p.master == nil {
		return false
	}
	if err := p.cmd.Process.Signal(syscall.Signal(0)); err != nil {
		return false
	}
	return true
}

func (p *PtySession) Running() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.runningLocked()
}

func (p *PtySession) Size() (cols, rows int, running bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	cols, rows = p.sizeLocked()
	return cols, rows, p.runningLocked()
}

func (p *PtySession) Write(b []byte) error {
	if len(b) == 0 {
		return nil
	}
	p.mu.Lock()
	m := p.master
	p.mu.Unlock()
	if m == nil {
		return fmt.Errorf("terminal is not running")
	}
	_, err := m.Write(b)
	return err
}

func (p *PtySession) SendInput(literal, named, ctrl string) error {
	b, err := encodePtyInput(literal, named, ctrl)
	if err != nil {
		return err
	}
	return p.Write(b)
}

func (p *PtySession) applyWinsizeLocked() {
	if p.master == nil {
		return
	}
	cols, rows := p.sizeLocked()
	ws := unix.Winsize{Row: uint16(rows), Col: uint16(cols)}
	_ = unix.IoctlSetWinsize(int(p.master.Fd()), unix.TIOCSWINSZ, &ws)
	if p.cmd != nil && p.cmd.Process != nil {
		if pgid, err := syscall.Getpgid(p.cmd.Process.Pid); err == nil {
			_ = syscall.Kill(-pgid, syscall.SIGWINCH)
		} else {
			_ = p.cmd.Process.Signal(syscall.SIGWINCH)
		}
	}
}

func (p *PtySession) Resize(cols, rows int) error {
	cols, rows = clampTermSize(cols, rows)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cols == cols && p.rows == rows {
		return nil
	}
	p.cols = cols
	p.rows = rows
	if p.runningLocked() {
		p.applyWinsizeLocked()
	}
	return nil
}

func (p *PtySession) broadcastLocked(ev ptyEvent) {
	for ch := range p.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

func (p *PtySession) killProcLocked() {
	if p.cmd != nil && p.cmd.Process != nil {
		if pgid, err := syscall.Getpgid(p.cmd.Process.Pid); err == nil {
			_ = syscall.Kill(-pgid, syscall.SIGHUP)
			time.Sleep(80 * time.Millisecond)
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		} else {
			_ = p.cmd.Process.Kill()
		}
	}
	if p.master != nil {
		_ = p.master.Close()
		p.master = nil
	}
	p.cmd = nil
}

func (p *PtySession) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.killProcLocked()
	p.buf = nil
	p.broadcastLocked(ptyEvent{reset: true})
	return nil
}

func (p *PtySession) Start(dir string, env []string, argv []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("no command")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.runningLocked() {
		return nil
	}
	p.killProcLocked()
	if dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("mkdir %s: %v", dir, err)
		}
	}
	cols, rows := p.sizeLocked()
	master, slave, err := openPty(cols, rows)
	if err != nil {
		return fmt.Errorf("open pty: %v", err)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdin = slave
	cmd.Stdout = slave
	cmd.Stderr = slave
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true,
		Setctty: true,
		Ctty:    0,
	}
	if err := cmd.Start(); err != nil {
		master.Close()
		slave.Close()
		return fmt.Errorf("starting %s: %v", argv[0], err)
	}
	_ = slave.Close()
	p.cmd = cmd
	p.master = master
	p.buf = nil
	p.broadcastLocked(ptyEvent{reset: true})
	go func(m *os.File, proc *exec.Cmd) {
		tmp := make([]byte, 4096)
		for {
			n, err := m.Read(tmp)
			if n > 0 {
				p.mu.Lock()
				if p.master == m {
					p.buf = append(p.buf, tmp[:n]...)
					if len(p.buf) > ptyMaxOut {
						cut := len(p.buf) - ptyMaxOut
						if i := indexByteFrom(p.buf, '\n', cut); i >= 0 && i < len(p.buf)-1 {
							cut = i + 1
						}
						p.buf = append([]byte(nil), p.buf[cut:]...)
					}
					p.broadcastLocked(ptyEvent{data: append([]byte(nil), tmp[:n]...)})
				}
				p.mu.Unlock()
			}
			if err != nil {
				break
			}
		}
		_ = proc.Wait()
		p.mu.Lock()
		if p.cmd == proc {
			if p.master == m {
				_ = m.Close()
				p.master = nil
			}
			p.cmd = nil
		}
		p.mu.Unlock()
	}(master, cmd)
	log.Printf("%s: started %v in %s (pid %d) %dx%d", p.name, argv, dir, cmd.Process.Pid, cols, rows)
	return nil
}

func (p *PtySession) Subscribe() (snapshot []byte, running bool, cols, rows int, ch chan ptyEvent, cancel func()) {
	ch = make(chan ptyEvent, 1024)
	p.mu.Lock()
	snapshot = append([]byte(nil), p.buf...)
	running = p.runningLocked()
	cols, rows = p.sizeLocked()
	p.subs[ch] = struct{}{}
	p.mu.Unlock()
	var once sync.Once
	cancel = func() {
		once.Do(func() {
			p.mu.Lock()
			if _, ok := p.subs[ch]; ok {
				delete(p.subs, ch)
				close(ch)
			}
			p.mu.Unlock()
		})
	}
	return
}

func ServePtyWS(p *PtySession, w http.ResponseWriter, r *http.Request) {
	conn, err := termWSUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("pty ws upgrade: %v", err)
		return
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		return nil
	})

	snap, running, cols, rows, ch, cancel := p.Subscribe()
	defer cancel()

	var wmu sync.Mutex
	writeJSON := func(v interface{}) error {
		wmu.Lock()
		defer wmu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		return conn.WriteJSON(v)
	}
	writeBin := func(b []byte) error {
		wmu.Lock()
		defer wmu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		return conn.WriteMessage(websocket.BinaryMessage, b)
	}

	if err := writeJSON(map[string]interface{}{
		"t":       "hello",
		"running": running,
		"cols":    cols,
		"rows":    rows,
	}); err != nil {
		return
	}
	if len(snap) > 0 {
		if err := writeBin(snap); err != nil {
			return
		}
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(20 * time.Second)
		defer tick.Stop()
		for {
			select {
			case ev, ok := <-ch:
				if !ok {
					return
				}
				if ev.reset {
					if writeJSON(map[string]string{"t": "reset"}) != nil {
						conn.Close()
						return
					}
					continue
				}
				if len(ev.data) == 0 {
					continue
				}
				if writeBin(ev.data) != nil {
					conn.Close()
					return
				}
			case <-tick.C:
				wmu.Lock()
				err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second))
				wmu.Unlock()
				if err != nil {
					conn.Close()
					return
				}
			}
		}
	}()

	for {
		mt, data, err := conn.ReadMessage()
		if err != nil {
			break
		}
		_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		if mt == websocket.TextMessage && len(data) > 0 && data[0] == '{' {
			var msg struct {
				T    string `json:"t"`
				Cols int    `json:"cols"`
				Rows int    `json:"rows"`
			}
			if json.Unmarshal(data, &msg) == nil && msg.T == "resize" {
				_ = p.Resize(msg.Cols, msg.Rows)
				continue
			}
		}
		_ = p.Write(data)
	}
	cancel()
	<-done
}
