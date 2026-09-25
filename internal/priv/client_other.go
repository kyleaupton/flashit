//go:build !unix

package priv

import (
	"net"
	"os"
)

func writeWithFDs(conn net.Conn, line []byte, _ []int) error {
	_, err := conn.Write(line)
	return err
}

// imageHandle is the image's handle value in this process; the helper
// duplicates it out of us.
func imageHandle(f *os.File) uint64 { return uint64(f.Fd()) }
