//go:build darwin

package priv

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/kyleaupton/flashit/internal/logger"
)

const (
	helperName = "flashit-helper"
	// How long a released helper gets to see EOF on its socket and leave on
	// its own before it is killed. It is the app's own child, same uid, so
	// killing it is always allowed.
	exitGrace = 5 * time.Second
)

// helperProcess is one spawned helper and the socketpair end it serves.
type helperProcess struct {
	conn net.Conn
	cmd  *exec.Cmd
	done chan error
}

// findHelper looks next to the running executable, which is Contents/MacOS
// in the bundle, then in its helpers/ directory. Never the working directory.
func findHelper() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	exeDir := filepath.Dir(exe)
	candidates := []string{
		filepath.Join(exeDir, helperName),
		filepath.Join(exeDir, "helpers", helperName),
	}
	for _, p := range candidates {
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			return p, nil
		}
	}
	return "", fmt.Errorf("helper not found at %v", candidates)
}

// spawnHelper starts the helper at path with one end of a socketpair on fd 3
// and returns the other end. The helper serves that socket until it reads
// EOF or goes idle; its stdout and stderr land in the app's log.
func spawnHelper(path string) (*helperProcess, error) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, err
	}
	syscall.CloseOnExec(fds[0])
	syscall.CloseOnExec(fds[1])
	app := os.NewFile(uintptr(fds[0]), "helper")
	child := os.NewFile(uintptr(fds[1]), "app")

	cmd := exec.Command(path)
	cmd.ExtraFiles = []*os.File{child}
	out := &helperLog{}
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		app.Close()
		child.Close()
		return nil, fmt.Errorf("start helper: %w", err)
	}
	child.Close()
	h := &helperProcess{cmd: cmd, done: make(chan error, 1)}
	go func() { h.done <- cmd.Wait() }()

	conn, err := net.FileConn(app)
	app.Close()
	if err != nil {
		h.release()
		return nil, err
	}
	h.conn = conn
	return h, nil
}

// release closes the session, which is the helper's cue to exit, and kills
// it if it has not left within exitGrace.
func (h *helperProcess) release() {
	if h.conn != nil {
		h.conn.Close()
		h.conn = nil
	}
	go func() {
		if !h.wait(exitGrace) {
			logger.Warn("helper did not exit after the session closed, killing it", "pid", h.cmd.Process.Pid)
			_ = h.cmd.Process.Kill()
		}
	}()
}

// wait blocks until the helper has exited or bound passes. The exit status
// is put back so later callers see it too.
func (h *helperProcess) wait(bound time.Duration) bool {
	select {
	case err := <-h.done:
		h.done <- err
		return true
	case <-time.After(bound):
		return false
	}
}

// helperLog forwards each line the helper prints to the app's logger.
type helperLog struct {
	buf []byte
}

func (w *helperLog) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			return len(p), nil
		}
		logger.Info("helper: " + string(w.buf[:i]))
		w.buf = w.buf[i+1:]
	}
}
