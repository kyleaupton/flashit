//go:build unix

package priv

import (
	"net"
	"syscall"
)

// writeWithFDs sends line and, when the connection is a unix socket, the
// descriptors as SCM_RIGHTS in the same message so the helper receives them
// with that request. Anywhere else the line goes alone and the helper
// refuses the op.
func writeWithFDs(conn net.Conn, line []byte, fds []int) error {
	mc, ok := conn.(interface {
		WriteMsgUnix(b, oob []byte, addr *net.UnixAddr) (n, oobn int, err error)
	})
	if !ok || len(fds) == 0 {
		_, err := conn.Write(line)
		return err
	}
	_, _, err := mc.WriteMsgUnix(line, syscall.UnixRights(fds...), nil)
	return err
}
