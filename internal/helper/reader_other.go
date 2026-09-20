//go:build !unix

package helper

import (
	"net"
	"os"
)

// No descriptor passing here: nothing arrives, so nothing needs closing.

func newLineReader(conn net.Conn) lineReader { return newPlainReader(conn) }

func takeFile([]int) *os.File { return nil }

func closeFDs([]int) {}
