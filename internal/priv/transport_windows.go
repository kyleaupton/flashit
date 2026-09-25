package priv

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"
	"unsafe"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"

	"github.com/kyleaupton/flashit/internal/logger"
)

// HelperFlag is the first argument that makes flashit.exe serve as the
// privileged helper instead of starting the app.
const HelperFlag = "--privileged-helper"

const (
	// UAC waits on the user; give them a while.
	spawnTimeout = 2 * time.Minute
	// After UAC, how long the helper has to create its pipe.
	pipeTimeout = 30 * time.Second
	// What the app asks for on the pipe: read, write, and the attributes
	// GetNamedPipeServerProcessId needs. Not GENERIC_WRITE, which would
	// include FILE_CREATE_PIPE_INSTANCE, which the helper does not grant.
	pipeAccess = windows.FILE_READ_DATA | windows.FILE_WRITE_DATA | windows.FILE_READ_ATTRIBUTES | windows.SYNCHRONIZE
)

// ErrAuthorizationCancelled is a declined or dismissed UAC prompt.
var ErrAuthorizationCancelled = errors.New("the administrator prompt was cancelled")

// helperProcess is one elevated helper and the connection to it. The
// process handle is kept until the helper is seen to exit: closing it
// earlier could let alive ask about an unrelated process.
type helperProcess struct {
	conn    net.Conn
	pid     uint32
	process windows.Handle
	log     *os.File

	mu     sync.Mutex
	exited bool
}

func (h *helperProcess) alive() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.exited {
		return false
	}
	ev, err := windows.WaitForSingleObject(h.process, 0)
	if err == nil && ev == uint32(windows.WAIT_TIMEOUT) {
		return true
	}
	h.exited = true
	windows.CloseHandle(h.process)
	return false
}

// release disconnects; the helper sees its only client leave and exits.
func (h *helperProcess) release() {
	if h.conn != nil {
		h.conn.Close()
		h.conn = nil
	}
	if h.log != nil {
		h.log.Close()
		h.log = nil
	}
}

// spawnHelper starts this executable elevated as the helper on a fresh
// random pipe, then connects and checks that the pipe's server is the
// process UAC started. On failure after UAC, the returned process is
// non-nil and already released, so the caller will not stack another.
func spawnHelper(ctx context.Context) (*helperProcess, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	pipe, err := newPipeName()
	if err != nil {
		return nil, err
	}
	args := HelperFlag + " -pipe " + pipe + " -parent-pid " + strconv.Itoa(os.Getpid())
	logFile, err := logger.OpenFile("helper.log")
	if err != nil {
		logger.Warn("helper log unavailable", "error", err)
	} else {
		args += " -log-handle " + strconv.FormatUint(uint64(logFile.Fd()), 10)
	}

	process, err := runElevated(ctx, exe, args)
	if err != nil {
		if logFile != nil {
			logFile.Close()
		}
		return nil, err
	}
	pid, err := windows.GetProcessId(process)
	if err != nil {
		windows.CloseHandle(process)
		if logFile != nil {
			logFile.Close()
		}
		return nil, fmt.Errorf("helper process id: %w", err)
	}
	h := &helperProcess{pid: pid, process: process, log: logFile}
	conn, err := dialHelper(ctx, pipe, h)
	if err != nil {
		h.release()
		return h, err
	}
	h.conn = conn
	return h, nil
}

func newPipeName() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return `\\.\pipe\flashit-` + hex.EncodeToString(b[:]), nil
}

// dialHelper connects to pipe once the helper has created it, and refuses
// a pipe served by any process but the helper: someone who created the name
// first would otherwise get the app's requests.
func dialHelper(ctx context.Context, pipe string, h *helperProcess) (net.Conn, error) {
	deadline := time.Now().Add(pipeTimeout)
	for {
		dctx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		conn, err := winio.DialPipeAccess(dctx, pipe, pipeAccess)
		cancel()
		if err == nil {
			if err := checkPipeServer(conn, h.pid); err != nil {
				conn.Close()
				return nil, err
			}
			return conn, nil
		}
		if !h.alive() {
			return nil, errors.New("the privileged helper exited before serving; see helper.log")
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out waiting for the helper pipe: %w", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// checkPipeServer refuses a connection whose server end is not process pid.
func checkPipeServer(conn net.Conn, pid uint32) error {
	ph, ok := conn.(interface{ Fd() uintptr })
	if !ok {
		return errors.New("not a named pipe connection")
	}
	var server uint32
	if err := windows.GetNamedPipeServerProcessId(windows.Handle(ph.Fd()), &server); err != nil {
		return fmt.Errorf("GetNamedPipeServerProcessId: %w", err)
	}
	if server != pid {
		return fmt.Errorf("the helper pipe is served by process %d, not the helper (%d); refusing it", server, pid)
	}
	return nil
}

var (
	shell32            = windows.NewLazySystemDLL("shell32.dll")
	procShellExecuteEx = shell32.NewProc("ShellExecuteExW")
)

// shellExecuteInfo is SHELLEXECUTEINFOW.
type shellExecuteInfo struct {
	cbSize      uint32
	fMask       uint32
	hwnd        windows.Handle
	verb        *uint16
	file        *uint16
	parameters  *uint16
	directory   *uint16
	show        int32
	instApp     windows.Handle
	idList      uintptr
	class       *uint16
	keyClass    windows.Handle
	hotKey      uint32
	iconMonitor windows.Handle
	process     windows.Handle
}

const (
	seeMaskNoCloseProcess = 0x00000040
	seeMaskNoAsync        = 0x00000100
	seeMaskFlagNoUI       = 0x00000400
)

type elevated struct {
	h   windows.Handle
	err error
}

// runElevated starts exe with args through the runas verb, which raises the
// UAC prompt, and returns the new process. ShellExecuteEx blocks until the
// user answers, so it runs on its own locked thread; a cancelled ctx stops
// the wait but not the prompt.
func runElevated(ctx context.Context, exe, args string) (windows.Handle, error) {
	done := make(chan elevated, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED|windows.COINIT_DISABLE_OLE1DDE); err == nil {
			defer windows.CoUninitialize()
		}
		info := shellExecuteInfo{
			fMask:      seeMaskNoCloseProcess | seeMaskNoAsync | seeMaskFlagNoUI,
			verb:       windows.StringToUTF16Ptr("runas"),
			file:       windows.StringToUTF16Ptr(exe),
			parameters: windows.StringToUTF16Ptr(args),
			show:       windows.SW_HIDE,
		}
		info.cbSize = uint32(unsafe.Sizeof(info))
		ok, _, err := procShellExecuteEx.Call(uintptr(unsafe.Pointer(&info)))
		switch {
		case ok == 0 && errors.Is(err, windows.ERROR_CANCELLED):
			done <- elevated{err: ErrAuthorizationCancelled}
		case ok == 0:
			done <- elevated{err: fmt.Errorf("start elevated helper: %w", err)}
		case info.process == 0:
			done <- elevated{err: errors.New("start elevated helper: no process handle")}
		default:
			done <- elevated{h: info.process}
		}
	}()

	timer := time.NewTimer(spawnTimeout)
	defer timer.Stop()
	select {
	case r := <-done:
		return r.h, r.err
	case <-ctx.Done():
		go abandon(done)
		return 0, ctx.Err()
	case <-timer.C:
		go abandon(done)
		return 0, errors.New("timed out waiting for the administrator prompt")
	}
}

// abandon waits out a prompt nobody is waiting for any more. A helper
// started that way never gets a client and exits on its idle timeout.
func abandon(done <-chan elevated) {
	if h := (<-done).h; h != 0 {
		windows.CloseHandle(h)
	}
}
