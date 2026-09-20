//go:build unix

package helpertest

import (
	"net"
	"os"
	"sync"
	"syscall"
	"testing"
)

// SocketPair returns both ends of a connected unix stream socket, so tests
// can pass file descriptors the way the app does. Data through a socket is
// invisible to the race detector, unlike net.Pipe's channel handoff, so
// both ends touch one mutex around every transfer: whatever a test did to
// a fake before sending happens-before the helper reads the request, and
// whatever the helper did before answering happens-before the test reads
// the answer.
func SocketPair(t *testing.T) (client, server net.Conn) {
	t.Helper()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	mu := &sync.Mutex{}
	conns := make([]*SyncConn, 2)
	for i, fd := range fds {
		f := os.NewFile(uintptr(fd), "socketpair")
		c, err := net.FileConn(f)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		uc, ok := c.(*net.UnixConn)
		if !ok {
			t.Fatalf("socketpair end is %T", c)
		}
		conns[i] = &SyncConn{UnixConn: uc, mu: mu}
	}
	t.Cleanup(func() { conns[0].Close(); conns[1].Close() })
	return conns[0], conns[1]
}

// SyncConn is a *net.UnixConn whose transfers are ordered by a mutex shared
// with the other end; see SocketPair.
type SyncConn struct {
	*net.UnixConn
	mu *sync.Mutex
}

func (c *SyncConn) sync() {
	c.mu.Lock()
	//lint:ignore SA2001 the empty critical section is the point
	c.mu.Unlock()
}

func (c *SyncConn) Read(b []byte) (int, error) {
	n, err := c.UnixConn.Read(b)
	c.sync()
	return n, err
}

func (c *SyncConn) ReadMsgUnix(b, oob []byte) (int, int, int, *net.UnixAddr, error) {
	n, oobn, flags, addr, err := c.UnixConn.ReadMsgUnix(b, oob)
	c.sync()
	return n, oobn, flags, addr, err
}

func (c *SyncConn) Write(b []byte) (int, error) {
	c.sync()
	return c.UnixConn.Write(b)
}

func (c *SyncConn) WriteMsgUnix(b, oob []byte, addr *net.UnixAddr) (int, int, error) {
	c.sync()
	return c.UnixConn.WriteMsgUnix(b, oob, addr)
}
