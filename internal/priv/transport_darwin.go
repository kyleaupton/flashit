//go:build darwin

package priv

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/kyleaupton/flashit/internal/logger"
)

const (
	// How long a released helper gets to see EOF on its socket and leave on
	// its own before it is killed. It is the app's own child, same uid, so
	// killing it is always allowed.
	exitGrace = 5 * time.Second
	// The helper's idle exit exists for the root helpers that outlive the
	// app's interest in them; this one is owned and killed by the app, and a
	// slow step between two ops must not lose it.
	helperIdle = "24h"
)

var installedHelperPaths []string

// spawnHelper starts the helper at path with one end of a socketpair on fd 3
// and returns the other end. The helper serves that socket until it reads
// EOF; its stdout and stderr land in the app's log.
func spawnHelper(path string) (*helperProcess, error) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, err
	}
	syscall.CloseOnExec(fds[0])
	syscall.CloseOnExec(fds[1])
	app := os.NewFile(uintptr(fds[0]), "helper")
	child := os.NewFile(uintptr(fds[1]), "app")

	cmd := exec.Command(path, "-idle", helperIdle)
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

// kill ends the helper at once: for one stuck behind a sheet nobody will
// answer, or one whose TCC verdict is cached and must not serve another op.
func (h *helperProcess) kill() {
	if h.conn != nil {
		h.conn.Close()
		h.conn = nil
	}
	_ = h.cmd.Process.Kill()
	h.wait(exitGrace)
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
