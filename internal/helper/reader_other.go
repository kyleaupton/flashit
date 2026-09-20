//go:build !unix

package helper

import "net"

func newLineReader(conn net.Conn) lineReader { return newPlainReader(conn) }
