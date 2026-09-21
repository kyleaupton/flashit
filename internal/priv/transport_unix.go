//go:build unix

package priv

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const helperName = "flashit-helper"

// helperProcess is one spawned helper and the connection it serves.
type helperProcess struct {
	conn net.Conn
	cmd  *exec.Cmd
	done chan error
	// dir is the private socket directory of a pkexec helper; the macOS
	// helper inherits its socket and has none.
	dir string
}

// findHelper looks next to the running executable, in its helpers/
// directory (the Taskfile's bin/helpers layout), then at the platform's
// installed paths. Never the working directory: on Linux pkexec would run
// whatever sits there as root.
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
	candidates = append(candidates, installedHelperPaths...)
	for _, p := range candidates {
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			return p, nil
		}
	}
	return "", fmt.Errorf("helper not found at %v", candidates)
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

func (h *helperProcess) alive() bool {
	select {
	case err := <-h.done:
		h.done <- err
		return false
	default:
		return true
	}
}
