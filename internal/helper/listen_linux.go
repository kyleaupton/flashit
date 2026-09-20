//go:build linux

package helper

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

// Listen binds the socket inside a directory the caller created with mode
// 0700. Nobody else can reach the directory, so there is no window between
// bind and chmod to race; the umask makes the socket 0600 from the start.
func Listen(path string, uid int) (net.Listener, error) {
	if uid < 0 {
		return nil, errors.New("no caller uid to hand the socket to")
	}
	dir := filepath.Dir(path)
	st, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", dir)
	}
	if st.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%s must be mode 0700, is %04o", dir, st.Mode().Perm())
	}
	if owner := int(st.Sys().(*syscall.Stat_t).Uid); owner != uid {
		return nil, fmt.Errorf("%s is owned by uid %d, expected %d", dir, owner, uid)
	}
	if _, err := os.Lstat(path); err == nil {
		return nil, fmt.Errorf("%s already exists", path)
	}

	old := unix.Umask(0o077)
	ln, err := net.Listen("unix", path)
	unix.Umask(old)
	if err != nil {
		return nil, err
	}
	if err := os.Chown(path, uid, -1); err != nil {
		ln.Close()
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return nil, err
	}
	return ln, nil
}
