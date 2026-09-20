package helper

import (
	"bufio"
	"errors"
	"net"
	"os"
	"syscall"
)

var errLineTooLong = errors.New("request line too long")

// message is one request line plus the file descriptors that arrived with
// it. The receiver owns the descriptors.
type message struct {
	line []byte
	fds  []int
}

type lineReader interface {
	next() (message, error)
}

// plainReader serves connections that cannot carry descriptors.
type plainReader struct {
	r *bufio.Reader
}

func (p *plainReader) next() (message, error) {
	line, err := p.r.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) {
		return message{}, errLineTooLong
	}
	if err != nil && len(line) == 0 {
		return message{}, err
	}
	return message{line: trimNewline(line)}, nil
}

func newPlainReader(conn net.Conn) lineReader {
	return &plainReader{r: bufio.NewReaderSize(conn, maxLineBytes)}
}

func trimNewline(line []byte) []byte {
	out := make([]byte, len(line))
	copy(out, line)
	for len(out) > 0 && (out[len(out)-1] == '\n' || out[len(out)-1] == '\r') {
		out = out[:len(out)-1]
	}
	return out
}

// takeFile keeps the first descriptor as an *os.File and closes the rest.
// An op is handed one image, never a choice of several.
func takeFile(fds []int) *os.File {
	var f *os.File
	for i, fd := range fds {
		if i == 0 {
			f = os.NewFile(uintptr(fd), "image")
			continue
		}
		syscall.Close(fd)
	}
	return f
}

func closeFDs(fds []int) {
	for _, fd := range fds {
		syscall.Close(fd)
	}
}
