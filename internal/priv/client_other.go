//go:build !unix

package priv

import "net"

func writeWithFDs(conn net.Conn, line []byte, _ []int) error {
	_, err := conn.Write(line)
	return err
}
