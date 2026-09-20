//go:build unix

package helper

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"syscall"
)

// maxPassedFDs bounds the ancillary buffer; anything beyond the first
// descriptor is closed anyway.
const maxPassedFDs = 8

// msgConn is what *net.UnixConn offers for receiving SCM_RIGHTS; tests wrap
// the connection and still satisfy it.
type msgConn interface {
	ReadMsgUnix(b, oob []byte) (n, oobn, flags int, addr *net.UnixAddr, err error)
}

func newLineReader(conn net.Conn) lineReader {
	if mc, ok := conn.(msgConn); ok {
		return &unixReader{conn: mc}
	}
	return newPlainReader(conn)
}

// unixReader reads with recvmsg so descriptors sent with SCM_RIGHTS stay
// attached to the request they came with: the kernel delivers them with
// the first bytes of the sendmsg that carried them, which is the start of
// that request's line.
type unixReader struct {
	conn    msgConn
	buf     []byte
	pending []int
	eof     bool
}

func (r *unixReader) next() (message, error) {
	chunk := make([]byte, 4096)
	oob := make([]byte, syscall.CmsgSpace(maxPassedFDs*4))
	for {
		if i := bytes.IndexByte(r.buf, '\n'); i >= 0 {
			m := message{line: trimNewline(r.buf[:i]), fds: r.pending}
			r.buf = r.buf[i+1:]
			r.pending = nil
			return m, nil
		}
		if len(r.buf) > maxLineBytes {
			closeFDs(r.pending)
			r.pending = nil
			return message{}, errLineTooLong
		}
		if r.eof {
			if len(r.buf) > 0 {
				m := message{line: trimNewline(r.buf), fds: r.pending}
				r.buf, r.pending = nil, nil
				return m, nil
			}
			closeFDs(r.pending)
			r.pending = nil
			return message{}, io.EOF
		}

		n, oobn, flags, _, err := r.conn.ReadMsgUnix(chunk, oob)
		if oobn > 0 {
			fds, perr := parseRights(oob[:oobn])
			r.pending = append(r.pending, fds...)
			if perr != nil {
				closeFDs(r.pending)
				r.pending = nil
				return message{}, perr
			}
		}
		if flags&syscall.MSG_CTRUNC != 0 {
			closeFDs(r.pending)
			r.pending = nil
			return message{}, errors.New("ancillary data truncated")
		}
		if n > 0 {
			r.buf = append(r.buf, chunk[:n]...)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				r.eof = true
				continue
			}
			closeFDs(r.pending)
			r.pending = nil
			return message{}, err
		}
	}
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

func parseRights(oob []byte) ([]int, error) {
	msgs, err := syscall.ParseSocketControlMessage(oob)
	if err != nil {
		return nil, fmt.Errorf("parse control message: %w", err)
	}
	var fds []int
	for _, m := range msgs {
		if m.Header.Level != syscall.SOL_SOCKET || m.Header.Type != syscall.SCM_RIGHTS {
			continue
		}
		got, err := syscall.ParseUnixRights(&m)
		if err != nil {
			closeFDs(fds)
			return nil, fmt.Errorf("parse SCM_RIGHTS: %w", err)
		}
		for _, fd := range got {
			syscall.CloseOnExec(fd)
		}
		fds = append(fds, got...)
	}
	return fds, nil
}
