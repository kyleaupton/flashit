//go:build unix

package priv

import (
	"net"
	"os/exec"
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
