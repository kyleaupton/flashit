//go:build darwin

package helper

import (
	"errors"
	"fmt"
	"net"
	"os"
)

// Listen binds the daemon's socket, world-connectable because caller
// authentication is what gates a connection, not the file mode. A stale
// socket from a previous daemon is replaced; anything else in the way is
// left alone.
func Listen(path string) (net.Listener, error) {
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("%w: %s exists and is not a socket", ErrConfig, path)
		}
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("remove stale socket: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o666); err != nil {
		ln.Close()
		return nil, err
	}
	return ln, nil
}
