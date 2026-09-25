package helper

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"regexp"
	"strconv"
	"time"
	"unsafe"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

// PipeNameRe is the only pipe name the helper listens on: random per spawn,
// chosen by the app.
var PipeNameRe = regexp.MustCompile(`^\\\\\.\\pipe\\flashit-[0-9a-f]{32}$`)

// ServeWindows runs flashit.exe as the privileged helper: started elevated
// by the app with the pipe name, the app's PID and optionally a handle to
// its log file, it serves that one app and exits when it leaves, goes idle
// or exits itself. It returns the process exit code.
func ServeWindows(args []string, version string) int {
	fs := flag.NewFlagSet("privileged-helper", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	pipe := fs.String("pipe", "", "")
	ppid := fs.Uint("parent-pid", 0, "")
	logHandle := fs.Uint64("log-handle", 0, "")
	idle := fs.Duration("idle", defaultIdleTimeout, "")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return 2
	}

	var logOut io.Writer = os.Stderr
	parent, perr := openParent(uint32(*ppid))
	if perr == nil && *logHandle != 0 {
		if f, err := parentLogFile(parent.dup, *logHandle); err == nil {
			defer f.Close()
			logOut = f
		}
	}
	log := slog.New(slog.NewTextHandler(logOut, nil)).With("helper", version, "pid", os.Getpid())
	if perr != nil {
		log.Error("refusing to serve", "error", perr)
		return 1
	}
	defer parent.close()

	if err := serveWindows(parent, *pipe, *idle, version, log); err != nil {
		if errors.Is(err, ErrIdle) {
			log.Info("idle, exiting")
			return 0
		}
		if errors.Is(err, context.Canceled) {
			log.Info("the app exited, exiting")
			return 0
		}
		log.Error("exiting", "error", err)
		return 1
	}
	log.Info("client left, exiting")
	return 0
}

func serveWindows(parent *parentProcess, pipe string, idle time.Duration, version string, log *slog.Logger) error {
	if !PipeNameRe.MatchString(pipe) {
		return fmt.Errorf("pipe name %q is not ours", pipe)
	}
	ln, err := listenForParent(pipe, parent)
	if err != nil {
		return err
	}
	defer ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		windows.WaitForSingleObject(parent.query, windows.INFINITE)
		cancel()
	}()

	disk, err := NewDisk(log)
	if err != nil {
		return err
	}
	log.Info("listening", "pipe", pipe, "parent", parent.pid, "idle", idle)
	srv := New(disk, pipeAuth{pid: parent.pid}, Options{
		Version:      version,
		IdleTimeout:  idle,
		Logger:       log,
		Images:       parentImages{parent: parent.dup},
		ExitOnReject: true,
	})
	return srv.Serve(ctx, ln)
}

// listenForParent creates the pipe as its first instance: it fails if the
// name exists, so nobody can have created it first. Only the parent's user
// may connect, and only locally.
func listenForParent(pipe string, parent *parentProcess) (net.Listener, error) {
	parentSID, err := tokenUserSID(parent.query)
	if err != nil {
		return nil, fmt.Errorf("app's user: %w", err)
	}
	selfSID, err := tokenUserSID(windows.CurrentProcess())
	if err != nil {
		return nil, fmt.Errorf("helper's user: %w", err)
	}
	ln, err := winio.ListenPipe(pipe, &winio.PipeConfig{
		SecurityDescriptor: PipeSDDL(parentSID, selfSID),
		InputBufferSize:    64 << 10,
		OutputBufferSize:   64 << 10,
	})
	if err != nil {
		return nil, fmt.Errorf("create pipe: %w", err)
	}
	return ln, nil
}

// PipeSDDL grants the app's user read and write on the pipe and nothing
// else. The helper's own user needs to create the pipe instances that
// Accept serves on; under over-the-shoulder elevation that is a different
// account, otherwise the same one. The medium label lets the unelevated app
// write to a pipe the elevated helper made.
func PipeSDDL(appSID, helperSID string) string {
	const clientAccess = windows.FILE_READ_DATA | windows.FILE_WRITE_DATA | windows.FILE_READ_ATTRIBUTES |
		windows.READ_CONTROL | windows.SYNCHRONIZE
	sddl := "D:P(A;;GA;;;" + helperSID + ")"
	if appSID != helperSID {
		sddl += "(A;;0x" + strconv.FormatUint(clientAccess, 16) + ";;;" + appSID + ")"
	}
	return sddl + "S:(ML;;NW;;;ME)"
}

// pipeAuth accepts only the parent: the client end's process must be the
// PID the helper was started for, which openParent proved is its parent and
// which cannot be reused while the helper holds a handle to it.
type pipeAuth struct {
	pid uint32
}

func (a pipeAuth) Authenticate(conn net.Conn) (Peer, error) {
	pid, err := PipeClientPID(conn)
	if err != nil {
		return Peer{}, fmt.Errorf("%w: %v", ErrUnauthorized, err)
	}
	if pid != a.pid {
		return Peer{}, fmt.Errorf("%w: client is process %d, expected %d", ErrUnauthorized, pid, a.pid)
	}
	return Peer{}, nil
}

// pipeHandle is how go-winio's pipe connections expose their handle.
type pipeHandle interface {
	Fd() uintptr
}

func PipeClientPID(conn net.Conn) (uint32, error) {
	ph, ok := conn.(pipeHandle)
	if !ok {
		return 0, errors.New("not a named pipe")
	}
	var pid uint32
	if err := windows.GetNamedPipeClientProcessId(windows.Handle(ph.Fd()), &pid); err != nil {
		return 0, fmt.Errorf("GetNamedPipeClientProcessId: %w", err)
	}
	return pid, nil
}

// parentProcess pins the app's process: while query is open its PID cannot
// be given to another process. dup carries PROCESS_DUP_HANDLE and nothing
// else, for taking the image handle.
type parentProcess struct {
	pid   uint32
	query windows.Handle
	dup   windows.Handle
}

func (p *parentProcess) close() {
	windows.CloseHandle(p.dup)
	windows.CloseHandle(p.query)
}

// openParent checks that pid is this process's parent: the PID the kernel
// recorded at creation, and a process created before this one, so a PID
// reused after the real parent exited is refused.
func openParent(pid uint32) (*parentProcess, error) {
	if pid == 0 {
		return nil, errors.New("no parent PID given")
	}
	var pbi windows.PROCESS_BASIC_INFORMATION
	if err := windows.NtQueryInformationProcess(windows.CurrentProcess(), windows.ProcessBasicInformation,
		unsafe.Pointer(&pbi), uint32(unsafe.Sizeof(pbi)), nil); err != nil {
		return nil, fmt.Errorf("own parent: %w", err)
	}
	if uint32(pbi.InheritedFromUniqueProcessId) != pid {
		return nil, fmt.Errorf("process %d is not the parent (the parent is %d)", pid, pbi.InheritedFromUniqueProcessId)
	}
	query, err := openProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, pid)
	if err != nil {
		return nil, err
	}
	parentStart, err := processStart(query)
	if err != nil {
		windows.CloseHandle(query)
		return nil, err
	}
	selfStart, err := processStart(windows.CurrentProcess())
	if err != nil {
		windows.CloseHandle(query)
		return nil, err
	}
	if parentStart >= selfStart {
		windows.CloseHandle(query)
		return nil, fmt.Errorf("process %d started after this one; its PID was reused", pid)
	}
	dup, err := openProcess(windows.PROCESS_DUP_HANDLE, pid)
	if err != nil {
		windows.CloseHandle(query)
		return nil, err
	}
	return &parentProcess{pid: pid, query: query, dup: dup}, nil
}

// openProcess opens the app's process. Under over-the-shoulder elevation it
// belongs to another user and only SeDebugPrivilege gets the helper in.
func openProcess(access, pid uint32) (windows.Handle, error) {
	h, err := windows.OpenProcess(access, false, pid)
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) && enableDebugPrivilege() == nil {
		h, err = windows.OpenProcess(access, false, pid)
	}
	if err != nil {
		return 0, fmt.Errorf("open process %d: %w", pid, err)
	}
	return h, nil
}

func enableDebugPrivilege() error {
	var tok windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY, &tok); err != nil {
		return err
	}
	defer tok.Close()
	var luid windows.LUID
	if err := windows.LookupPrivilegeValue(nil, windows.StringToUTF16Ptr("SeDebugPrivilege"), &luid); err != nil {
		return err
	}
	tp := windows.Tokenprivileges{PrivilegeCount: 1}
	tp.Privileges[0] = windows.LUIDAndAttributes{Luid: luid, Attributes: windows.SE_PRIVILEGE_ENABLED}
	return windows.AdjustTokenPrivileges(tok, false, &tp, 0, nil, nil)
}

func processStart(h windows.Handle) (int64, error) {
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &created, &exited, &kernel, &user); err != nil {
		return 0, fmt.Errorf("GetProcessTimes: %w", err)
	}
	return created.Nanoseconds(), nil
}

func tokenUserSID(process windows.Handle) (string, error) {
	var tok windows.Token
	if err := windows.OpenProcessToken(process, windows.TOKEN_QUERY, &tok); err != nil {
		return "", err
	}
	defer tok.Close()
	u, err := tok.GetTokenUser()
	if err != nil {
		return "", err
	}
	return u.User.Sid.String(), nil
}

// parentLogFile is the log file the app opened for the helper, duplicated
// like the image. Writing to a path of the app's choosing as admin would
// let the app aim the helper at any file.
func parentLogFile(parent windows.Handle, handle uint64) (*os.File, error) {
	h, err := dupFromParent(parent, handle, 0, windows.DUPLICATE_SAME_ACCESS)
	if err != nil {
		return nil, err
	}
	t, err := windows.GetFileType(h)
	if err != nil || t != windows.FILE_TYPE_DISK {
		windows.CloseHandle(h)
		return nil, fmt.Errorf("log handle is not a file (type %d, %v)", t, err)
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
		windows.CloseHandle(h)
		return nil, fmt.Errorf("log handle is not a file: %v", err)
	}
	return os.NewFile(uintptr(h), "helper.log"), nil
}
