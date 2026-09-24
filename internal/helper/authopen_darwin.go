//go:build darwin

package helper

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kyleaupton/flashit/internal/proto"
)

const (
	authopenPath = "/usr/libexec/authopen"
	// authopen answers at once when the form already carries the right. A
	// wait this long means it raised a sheet of its own, which a grant that
	// passed Authorize must never cause; the process is killed rather than
	// left holding a sheet the user cannot attribute.
	authopenTimeout = 2 * time.Minute
)

// authopen runs bin (normally /usr/libexec/authopen) for path with the
// external form of an authorized ref on its stdin and returns the descriptor
// it sends back with SCM_RIGHTS. One socketpair end is both its stdin, where
// -extauth reads the form, and its stdout, where -stdoutpipe sends the
// descriptor. Exit 1 with "Operation not permitted" after a good sheet is
// the Removable Volumes refusal and is reported as tcc_denied.
func authopen(bin, path string, flags int, form []byte) (*os.File, error) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, err
	}
	syscall.CloseOnExec(fds[0])
	syscall.CloseOnExec(fds[1])
	parent := os.NewFile(uintptr(fds[0]), "authopen")
	child := os.NewFile(uintptr(fds[1]), "authopen")

	cmd := exec.Command(bin, "-stdoutpipe", "-extauth", "-o", strconv.Itoa(flags), path)
	cmd.Stdin, cmd.Stdout = child, child
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		parent.Close()
		child.Close()
		return nil, fmt.Errorf("start authopen: %w", err)
	}
	child.Close()
	conn, err := net.FileConn(parent)
	parent.Close()
	if err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return nil, err
	}
	uc := conn.(*net.UnixConn)
	defer uc.Close()

	if _, err := uc.Write(form); err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return nil, fmt.Errorf("send authorization to authopen: %w", err)
	}
	_ = uc.SetReadDeadline(time.Now().Add(authopenTimeout))
	status := make([]byte, 64)
	oob := make([]byte, syscall.CmsgSpace(maxPassedFDs*4))
	n, oobn, rflags, _, rerr := uc.ReadMsgUnix(status, oob)
	var received []int
	if oobn > 0 && rflags&syscall.MSG_CTRUNC == 0 {
		received, _ = parseRights(oob[:oobn])
	}
	if errors.Is(rerr, os.ErrDeadlineExceeded) {
		cmd.Process.Kill()
	}
	werr := cmd.Wait()

	if len(received) > 0 {
		f := os.NewFile(uintptr(received[0]), path)
		closeFDs(received[1:])
		return f, nil
	}
	msg := strings.TrimSpace(stderr.String())
	if errors.Is(rerr, os.ErrDeadlineExceeded) {
		return nil, fmt.Errorf("authopen did not answer within %s: %s", authopenTimeout, msg)
	}
	var exit *exec.ExitError
	if errors.As(werr, &exit) && exit.ExitCode() == 1 && strings.Contains(msg, "Operation not permitted") {
		return nil, proto.Errorf(proto.CodeTCCDenied, "FlashIt was denied access to removable volumes (%s)", path)
	}
	return nil, fmt.Errorf("authopen exited (%v) with status % x: %s", werr, status[:n], msg)
}
