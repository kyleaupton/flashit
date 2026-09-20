//go:build !unix

package helpertest

import (
	"net"
	"testing"
)

func SocketPair(t *testing.T) (client, server net.Conn) {
	t.Skip("descriptor passing needs a unix socket")
	return nil, nil
}
