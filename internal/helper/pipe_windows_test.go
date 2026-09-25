package helper

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"

	"github.com/kyleaupton/flashit/internal/proto"
)

func testPipeName(t *testing.T) string {
	t.Helper()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf(`\\.\pipe\flashit-%x`, b)
}

// testParent pins the test binary's real parent, as the helper pins the app.
func testParent(t *testing.T) *parentProcess {
	t.Helper()
	p, err := openParent(uint32(os.Getppid()))
	if err != nil {
		t.Fatalf("openParent(ppid): %v", err)
	}
	t.Cleanup(p.close)
	return p
}

func dial(t *testing.T, pipe string) net.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := winio.DialPipeAccess(ctx, pipe, windows.FILE_READ_DATA|windows.FILE_WRITE_DATA|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE)
	if err != nil {
		t.Fatalf("dial %s: %v", pipe, err)
	}
	t.Cleanup(func() { conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	return conn
}

func readResponse(t *testing.T, r *bufio.Reader) proto.Response {
	t.Helper()
	line, err := r.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var resp proto.Response
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("decode %s: %v", line, err)
	}
	return resp
}

func ping(t *testing.T, conn net.Conn, r *bufio.Reader) proto.Response {
	t.Helper()
	req, _ := proto.NewRequest("p", proto.OpPing, proto.PingParams{Protocol: proto.ProtocolVersion})
	b, _ := json.Marshal(req)
	if _, err := conn.Write(append(b, '\n')); err != nil {
		t.Fatalf("write: %v", err)
	}
	return readResponse(t, r)
}

// startServer listens on a fresh pipe the way the helper does and serves
// with auth expecting clientPID.
func startServer(t *testing.T, clientPID uint32) (string, <-chan error) {
	t.Helper()
	pipe := testPipeName(t)
	ln, err := listenForParent(pipe, testParent(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	srv := New(noDisk{}, pipeAuth{pid: clientPID}, Options{
		Version: "test", IdleTimeout: 10 * time.Second, ExitOnReject: true,
	})
	done := make(chan error, 1)
	go func() { done <- srv.Serve(context.Background(), ln) }()
	return pipe, done
}

// noDisk fails every op; these tests only ping.
type noDisk struct{}

var errNoDisk = errors.New("no disk in this test")

func (noDisk) Stat(string) (DeviceInfo, error)          { return DeviceInfo{}, errNoDisk }
func (noDisk) SystemDisks() ([]string, error)           { return nil, errNoDisk }
func (noDisk) Partitions(string) ([]string, error)      { return nil, errNoDisk }
func (noDisk) Unmount(string) error                     { return errNoDisk }
func (noDisk) OpenRaw(string, Grant) (RawDevice, error) { return nil, errNoDisk }
func (noDisk) Eject(string) error                       { return errNoDisk }
func (noDisk) Format(context.Context, string, string, string) (string, error) {
	return "", errNoDisk
}

func waitServe(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return")
		return nil
	}
}

func TestPipeServesTheParentOnce(t *testing.T) {
	pipe, done := startServer(t, uint32(os.Getpid()))
	first := dial(t, pipe)
	fr := bufio.NewReader(first)
	if resp := ping(t, first, fr); resp.Type != proto.TypeResult {
		t.Fatalf("first client: %+v", resp)
	}

	second := dial(t, pipe)
	if resp := readResponse(t, bufio.NewReader(second)); resp.Code != proto.CodeBusy {
		t.Fatalf("second client: %+v", resp)
	}
	if resp := ping(t, first, fr); resp.Type != proto.TypeResult {
		t.Fatalf("first client after the second: %+v", resp)
	}

	first.Close()
	if err := waitServe(t, done); err != nil {
		t.Fatalf("Serve after the client left: %v", err)
	}
}

func TestPipeRefusesOtherProcess(t *testing.T) {
	// The test process is the client, but the helper expects its parent.
	pipe, done := startServer(t, uint32(os.Getppid()))
	conn := dial(t, pipe)
	if resp := readResponse(t, bufio.NewReader(conn)); resp.Code != proto.CodeUnauthorized {
		t.Fatalf("got %+v", resp)
	}
	if err := waitServe(t, done); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Serve returned %v, want it to exit unauthorized", err)
	}
}

// A child process connecting to a helper that serves this process is
// refused, and the helper exits.
func TestPipeRefusesChildProcess(t *testing.T) {
	pipe, done := startServer(t, uint32(os.Getpid()))
	cmd := exec.Command(os.Args[0], "-test.run=^TestPipeChildClient$")
	cmd.Env = append(os.Environ(), "FLASHIT_CHILD_PIPE="+pipe)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "code="+string(proto.CodeUnauthorized)) {
		t.Fatalf("child was not refused:\n%s", out)
	}
	if err := waitServe(t, done); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Serve returned %v", err)
	}
}

func TestPipeChildClient(t *testing.T) {
	pipe := os.Getenv("FLASHIT_CHILD_PIPE")
	if pipe == "" {
		t.Skip("run by TestPipeRefusesChildProcess")
	}
	conn := dial(t, pipe)
	resp := readResponse(t, bufio.NewReader(conn))
	fmt.Printf("code=%s\n", resp.Code)
}

// go-winio creates the first instance with FILE_CREATE, so a name someone
// else created first makes the helper fail instead of sharing it.
func TestPipeRefusesExistingName(t *testing.T) {
	pipe := testPipeName(t)
	squatter, err := winio.ListenPipe(pipe, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer squatter.Close()
	if ln, err := listenForParent(pipe, testParent(t)); err == nil {
		ln.Close()
		t.Fatal("listened on a pipe that already existed")
	}
}

// Every instance carries FILE_PIPE_REJECT_REMOTE_CLIENTS: the same pipe
// reached over SMB loopback is refused.
func TestPipeRejectsRemoteClients(t *testing.T) {
	pipe, _ := startServer(t, uint32(os.Getpid()))
	host, _ := os.Hostname()
	remote := strings.Replace(pipe, `\\.\`, `\\`+host+`\`, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := winio.DialPipeAccess(ctx, remote, windows.FILE_READ_DATA|windows.FILE_WRITE_DATA|windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE)
	if err == nil {
		conn.Close()
		t.Fatal("a remote client connected")
	}
	if errors.Is(err, windows.ERROR_BAD_NETPATH) || errors.Is(err, windows.ERROR_BAD_NET_NAME) {
		t.Skipf("no SMB server to loop through: %v", err)
	}
	t.Logf("remote dial refused: %v", err)
}

func TestPipeSDDL(t *testing.T) {
	sid, err := tokenUserSID(windows.CurrentProcess())
	if err != nil {
		t.Fatal(err)
	}
	for _, sddl := range []string{PipeSDDL(sid, sid), PipeSDDL(sid, "S-1-5-32-544")} {
		if _, err := winio.SddlToSecurityDescriptor(sddl); err != nil {
			t.Fatalf("%s: %v", sddl, err)
		}
	}
	if got := PipeSDDL("S-1-5-21-1", "S-1-5-21-2"); !strings.Contains(got, "(A;;0x120083;;;S-1-5-21-1)") || strings.Contains(got, "WD") {
		t.Fatalf("SDDL %s", got)
	}
}

func TestOpenParent(t *testing.T) {
	p, err := openParent(uint32(os.Getppid()))
	if err != nil {
		t.Fatalf("real parent refused: %v", err)
	}
	p.close()
	for name, pid := range map[string]uint32{
		"self":   uint32(os.Getpid()),
		"none":   0,
		"system": 4,
	} {
		if p, err := openParent(pid); err == nil {
			p.close()
			t.Errorf("%s (%d) accepted as the parent", name, pid)
		}
	}
}

func TestPipeClientPIDNeedsAPipe(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	if _, err := PipeClientPID(a); err == nil {
		t.Fatal("got a PID from something that is not a named pipe")
	}
}

func selfDup(t *testing.T) parentImages {
	t.Helper()
	h, err := windows.OpenProcess(windows.PROCESS_DUP_HANDLE, false, windows.GetCurrentProcessId())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { windows.CloseHandle(h) })
	return parentImages{parent: h}
}

func wantImageCode(t *testing.T, err error, code proto.ErrorCode) {
	t.Helper()
	var pe *proto.Error
	if !errors.As(err, &pe) || pe.Code != code {
		t.Fatalf("got %v, want %s", err, code)
	}
}

func TestImageHandle(t *testing.T) {
	images := selfDup(t)
	path := filepath.Join(t.TempDir(), "x.iso")
	if err := os.WriteFile(path, make([]byte, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	img, err := images.Image(uint64(f.Fd()), 4096)
	if err != nil {
		t.Fatalf("regular file refused: %v", err)
	}
	if n, err := img.ReadAt(make([]byte, 4096), 0); err != nil || n != 4096 {
		t.Fatalf("read the duplicated handle: %d, %v", n, err)
	}
	img.Close()

	_, err = images.Image(uint64(f.Fd()), 4095)
	wantImageCode(t, err, proto.CodeSizeMismatch)

	dir, err := windows.CreateFile(windows.StringToUTF16Ptr(t.TempDir()), windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(dir)
	_, err = images.Image(uint64(dir), 0)
	wantImageCode(t, err, proto.CodeInvalidSource)

	var r, w windows.Handle
	if err := windows.CreatePipe(&r, &w, nil, 0); err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(r)
	defer windows.CloseHandle(w)
	_, err = images.Image(uint64(r), 4096)
	wantImageCode(t, err, proto.CodeInvalidSource)

	nul, err := windows.CreateFile(windows.StringToUTF16Ptr(`\\.\NUL`), windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(nul)
	_, err = images.Image(uint64(nul), 4096)
	wantImageCode(t, err, proto.CodeInvalidSource)

	proc, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, windows.GetCurrentProcessId())
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(proc)
	_, err = images.Image(uint64(proc), 4096)
	wantImageCode(t, err, proto.CodeInvalidSource)

	for _, bad := range []uint64{0, 3, 1<<32 + 4, uint64(^uintptr(0))} {
		_, err = images.Image(bad, 4096)
		wantImageCode(t, err, proto.CodeInvalidSource)
	}
}

func TestFileBelowDevice(t *testing.T) {
	for nt, want := range map[string]bool{
		`\Device\HarddiskVolume3\Users\me\x.iso`: true,
		`\Device\Mup\server\share\x.iso`:         true,
		`\Device\HarddiskVolume3`:                false,
		`\Device\HarddiskVolume3\`:               false,
		`\Device\`:                               false,
		`C:\x.iso`:                               false,
		`\Device\HarddiskVolume3\dir\`:           false,
	} {
		if got := fileBelowDevice(nt); got != want {
			t.Errorf("fileBelowDevice(%q) = %v", nt, got)
		}
	}
}
