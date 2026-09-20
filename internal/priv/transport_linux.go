//go:build linux

package priv

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const (
	helperName      = "flashit-helper"
	installedHelper = "/usr/libexec/flashit/flashit-helper"
	// The polkit dialog blocks until the user answers; give them a while.
	spawnTimeout = 2 * time.Minute
	// The helper's default -idle, which the app does not override, plus a
	// margin: how long a released helper is given to leave on its own.
	exitGrace = 70 * time.Second
)

// helperProcess is one pkexec-spawned helper and the socket it serves.
type helperProcess struct {
	conn net.Conn
	cmd  *exec.Cmd
	dir  string
	done chan error
}

// findHelper looks next to the running executable, in its helpers/
// directory (the Taskfile's bin/helpers layout), then at the packaged path.
// Never the working directory: pkexec would run whatever sits there as root.
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
		installedHelper,
	}
	for _, p := range candidates {
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			return p, nil
		}
	}
	return "", fmt.Errorf("helper not found at %v", candidates)
}

// spawnHelper elevates the helper through pkexec on a fresh private socket
// directory and connects once the socket appears. The helper checks that the
// directory is ours and 0700 before it binds. On failure after pkexec
// started, the returned process is non-nil and already released so the
// caller can keep it from being spawned twice.
func spawnHelper(ctx context.Context) (*helperProcess, error) {
	path, err := findHelper()
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(runtimeDir(), "flashit-helper-")
	if err != nil {
		return nil, err
	}
	socket := filepath.Join(dir, "helper.sock")

	cmd := exec.Command("pkexec", path, "-socket", socket)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		os.RemoveAll(dir)
		return nil, fmt.Errorf("pkexec: %w", err)
	}
	h := &helperProcess{cmd: cmd, dir: dir, done: make(chan error, 1)}
	go func() { h.done <- cmd.Wait() }()

	if err := h.waitForSocket(ctx, socket); err != nil {
		h.release()
		return h, err
	}
	conn, err := net.Dial("unix", socket)
	if err != nil {
		h.release()
		return h, fmt.Errorf("connect to helper: %w", err)
	}
	h.conn = conn
	return h, nil
}

func runtimeDir() string {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			return d
		}
	}
	return os.TempDir()
}

func (h *helperProcess) waitForSocket(ctx context.Context, socket string) error {
	timeout := time.After(spawnTimeout)
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout:
			return errors.New("timed out waiting for the helper socket")
		case err := <-h.done:
			h.done <- err
			return spawnError(err)
		case <-tick.C:
			if _, err := os.Stat(socket); err == nil {
				return nil
			}
		}
	}
}

// spawnError turns pkexec's exit codes into something the user can act on:
// 126 is dismissed or denied, 127 is a missing helper or a polkit failure.
func spawnError(err error) error {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		switch exit.ExitCode() {
		case 126:
			return errors.New("authorization was denied or dismissed")
		case 127:
			return errors.New("pkexec could not start the helper")
		}
	}
	if err == nil {
		return errors.New("helper exited before serving")
	}
	return fmt.Errorf("helper exited: %w", err)
}

// release disconnects and lets the helper leave on its own: it runs as root
// under pkexec, so the app cannot kill it. The socket directory is removed
// only once the helper has exited; if it outlives the grace period the
// directory stays and the next EnsureReady keeps refusing to spawn another.
func (h *helperProcess) release() {
	if h.conn != nil {
		h.conn.Close()
		h.conn = nil
	}
	go func() {
		if h.wait(exitGrace) {
			os.RemoveAll(h.dir)
		}
	}()
}

// wait blocks until the pkexec child has exited or bound passes. The exit
// status is put back so later callers see it too.
func (h *helperProcess) wait(bound time.Duration) bool {
	select {
	case err := <-h.done:
		h.done <- err
		return true
	case <-time.After(bound):
		return false
	}
}

func (h *helperProcess) alive() bool {
	select {
	case err := <-h.done:
		h.done <- err
		return false
	default:
		return true
	}
}
