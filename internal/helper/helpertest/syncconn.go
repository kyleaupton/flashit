package helpertest

import (
	"net"
	"sync"
)

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
