package priv

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kyleaupton/flashit/internal/helper"
	"github.com/kyleaupton/flashit/internal/helper/helpertest"
	"github.com/kyleaupton/flashit/internal/proto"
)

// connect runs the real helper server on a fake disk and returns a client
// talking to it over a pipe.
func connect(t *testing.T, disk *helpertest.FakeDisk) (*Client, <-chan error) {
	t.Helper()
	srv := helper.New(disk, helpertest.FakeAuth{}, helper.Options{
		Version: "test", WriteBufferSize: 1024, ProgressInterval: time.Nanosecond,
	})
	cc, sc := helpertest.SocketPair(t)
	done := make(chan error, 1)
	go func() { done <- srv.ServeConn(context.Background(), sc, helper.Peer{UID: os.Getuid()}) }()
	c := NewClient(cc)
	t.Cleanup(func() { c.Close() })
	return c, done
}

func openSource(t *testing.T, size int) *os.File {
	t.Helper()
	f, err := os.Open(sourceFile(t, size))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func ctxTimeout(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func sourceFile(t *testing.T, size int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "x.iso")
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i * 7)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func wantCode(t *testing.T, err error, code proto.ErrorCode) {
	t.Helper()
	var pe *proto.Error
	if !errors.As(err, &pe) {
		t.Fatalf("expected *proto.Error %s, got %v", code, err)
	}
	if pe.Code != code {
		t.Fatalf("expected %s, got %s (%s)", code, pe.Code, pe.Message)
	}
}

func TestClientPing(t *testing.T) {
	c, _ := connect(t, helpertest.NewFakeDisk())
	r, err := c.Ping(ctxTimeout(t))
	if err != nil {
		t.Fatal(err)
	}
	if r.Protocol != proto.ProtocolVersion || r.Version != "test" {
		t.Fatalf("ping result %+v", r)
	}
}

func TestClientVersionMismatch(t *testing.T) {
	// A helper of another generation answers the handshake with an error and
	// hangs up; the client must surface the code, not a decode failure.
	cc, sc := net.Pipe()
	go func() {
		line, _ := bufio.NewReader(sc).ReadBytes('\n')
		var req proto.Request
		_ = json.Unmarshal(line, &req)
		b, _ := json.Marshal(proto.ErrorResponse(req.ID, proto.NewError(proto.CodeVersionMismatch, "old helper")))
		_, _ = sc.Write(append(b, '\n'))
		sc.Close()
	}()
	c := NewClient(cc)
	defer c.Close()
	_, err := c.Ping(ctxTimeout(t))
	wantCode(t, err, proto.CodeVersionMismatch)
}

func TestClientWriteImage(t *testing.T) {
	disk := helpertest.NewFakeDisk()
	c, _ := connect(t, disk)
	ctx := ctxTimeout(t)
	if _, err := c.Ping(ctx); err != nil {
		t.Fatal(err)
	}

	const size = 8192 + 100
	path := sourceFile(t, size)
	src, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	var calls int
	var last uint64
	err = c.WriteImage(ctx, proto.WriteImageParams{Device: helpertest.Removable, Size: size}, src, func(written, total uint64) {
		calls++
		if written < last || total != size {
			t.Errorf("bad progress %d/%d after %d", written, total, last)
		}
		last = written
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls == 0 || last != size {
		t.Fatalf("progress calls=%d last=%d", calls, last)
	}
	want, _ := os.ReadFile(path)
	if got := disk.Raw.Bytes(); string(got) != string(want) {
		t.Fatalf("device holds %d bytes, want %d", len(got), len(want))
	}
	if disk.Raw.Syncs != 1 {
		t.Fatalf("syncs = %d", disk.Raw.Syncs)
	}
}

func TestClientWriteImageRefused(t *testing.T) {
	disk := helpertest.NewFakeDisk()
	c, _ := connect(t, disk)
	ctx := ctxTimeout(t)
	if _, err := c.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	src := openSource(t, 4096)

	wantCode(t, c.WriteImage(ctx, proto.WriteImageParams{Device: "/dev/../etc/shadow", Size: 4096}, src, nil), proto.CodeInvalidDevice)
	wantCode(t, c.WriteImage(ctx, proto.WriteImageParams{Device: helpertest.Internal, Size: 4096}, src, nil), proto.CodeNotRemovable)
	wantCode(t, c.WriteImage(ctx, proto.WriteImageParams{Device: helpertest.System, Size: 4096}, src, nil), proto.CodeSystemDisk)
	wantCode(t, c.WriteImage(ctx, proto.WriteImageParams{Device: helpertest.Removable, Size: 4095}, src, nil), proto.CodeSizeMismatch)
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	defer pw.Close()
	wantCode(t, c.WriteImage(ctx, proto.WriteImageParams{Device: helpertest.Removable, Size: 4096}, pr, nil), proto.CodeInvalidSource)
	wantCode(t, c.WriteImage(ctx, proto.WriteImageParams{Device: helpertest.Removable, Size: helpertest.RemovableSize + 1}, src, nil), proto.CodeSizeMismatch)
	if disk.Raw != nil && len(disk.Raw.Bytes()) != 0 {
		t.Fatal("refused writes reached the device")
	}
	// Still connected and usable.
	if err := c.WriteImage(ctx, proto.WriteImageParams{Device: helpertest.Removable, Size: 4096}, src, nil); err != nil {
		t.Fatal(err)
	}
}

func TestClientFormatUnmountEject(t *testing.T) {
	disk := helpertest.NewFakeDisk()
	c, _ := connect(t, disk)
	ctx := ctxTimeout(t)
	if _, err := c.Ping(ctx); err != nil {
		t.Fatal(err)
	}

	mp, err := c.FormatDisk(ctx, proto.FormatDiskParams{Device: helpertest.Removable, Filesystem: "FAT32", Label: "FLASHIT"})
	if err != nil {
		t.Fatal(err)
	}
	if mp != disk.Mountpoint {
		t.Fatalf("mountpoint %q", mp)
	}
	_, err = c.FormatDisk(ctx, proto.FormatDiskParams{Device: helpertest.Removable, Filesystem: "FAT32", Label: "bad;label"})
	wantCode(t, err, proto.CodeInvalidLabel)
	_, err = c.FormatDisk(ctx, proto.FormatDiskParams{Device: helpertest.Removable, Filesystem: "ntfs", Label: "X"})
	wantCode(t, err, proto.CodeInvalidRequest)

	if err := c.Unmount(ctx, helpertest.Removable); err != nil {
		t.Fatal(err)
	}
	wantCode(t, c.Unmount(ctx, helpertest.System), proto.CodeSystemDisk)

	if err := c.Eject(ctx, helpertest.RemovableLink); err != nil {
		t.Fatal(err)
	}
	if len(disk.Ejected) != 1 || disk.Ejected[0] != helpertest.Removable {
		t.Fatalf("ejected %v", disk.Ejected)
	}
	wantCode(t, c.Eject(ctx, helpertest.Removable+"1"), proto.CodeInvalidDevice)
}

func TestClientContextCancelStopsWrite(t *testing.T) {
	disk := helpertest.NewFakeDisk()
	disk.Raw = &helpertest.FakeRaw{Started: make(chan struct{}), Block: make(chan struct{})}
	c, _ := connect(t, disk)
	if _, err := c.Ping(ctxTimeout(t)); err != nil {
		t.Fatal(err)
	}

	const size = 16384
	src := openSource(t, size)
	ctx, cancel := context.WithCancel(ctxTimeout(t))
	result := make(chan error, 1)
	go func() {
		result <- c.WriteImage(ctx, proto.WriteImageParams{Device: helpertest.Removable, Size: size}, src, nil)
	}()

	<-disk.Raw.Started
	cancel()
	// The helper is stuck in a device write; the client must wait for its
	// answer rather than return while the write goes on.
	select {
	case err := <-result:
		t.Fatalf("returned %v before the helper acknowledged", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(disk.Raw.Block)

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("write did not return after cancel")
	}
	if n := len(disk.Raw.Bytes()); n >= size {
		t.Fatalf("write ran to completion: %d bytes", n)
	}

	// The session survives a cancelled op.
	if err := c.Eject(ctxTimeout(t), helpertest.Removable); err != nil {
		t.Fatal(err)
	}
}

func TestClientExplicitCancel(t *testing.T) {
	disk := helpertest.NewFakeDisk()
	disk.Raw = &helpertest.FakeRaw{Started: make(chan struct{}), Block: make(chan struct{})}
	c, _ := connect(t, disk)
	ctx := ctxTimeout(t)
	if _, err := c.Ping(ctx); err != nil {
		t.Fatal(err)
	}

	const size = 16384
	src := openSource(t, size)
	result := make(chan error, 1)
	go func() {
		result <- c.WriteImage(ctx, proto.WriteImageParams{Device: helpertest.Removable, Size: size}, src, nil)
	}()
	<-disk.Raw.Started
	if err := c.Cancel(ctx); err != nil {
		t.Fatal(err)
	}
	close(disk.Raw.Block)
	wantCode(t, <-result, proto.CodeCancelled)

	if err := c.Cancel(ctx); err != nil {
		t.Fatalf("cancel with nothing in flight: %v", err)
	}
}

func TestClientBusy(t *testing.T) {
	disk := helpertest.NewFakeDisk()
	disk.Raw = &helpertest.FakeRaw{Started: make(chan struct{}), Block: make(chan struct{})}
	c, _ := connect(t, disk)
	ctx := ctxTimeout(t)
	if _, err := c.Ping(ctx); err != nil {
		t.Fatal(err)
	}

	const size = 4096
	src := openSource(t, size)
	result := make(chan error, 1)
	go func() {
		result <- c.WriteImage(ctx, proto.WriteImageParams{Device: helpertest.Removable, Size: size}, src, nil)
	}()
	<-disk.Raw.Started
	wantCode(t, c.Eject(ctx, helpertest.Removable), proto.CodeBusy)
	close(disk.Raw.Block)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func TestClientHelperGoesAway(t *testing.T) {
	disk := helpertest.NewFakeDisk()
	disk.Raw = &helpertest.FakeRaw{Started: make(chan struct{}), Block: make(chan struct{})}
	srv := helper.New(disk, helpertest.FakeAuth{}, helper.Options{WriteBufferSize: 1024})
	cc, sc := helpertest.SocketPair(t)
	serveCtx, stopServer := context.WithCancel(context.Background())
	defer stopServer()
	go func() { _ = srv.ServeConn(serveCtx, sc, helper.Peer{UID: os.Getuid()}) }()
	c := NewClient(cc)
	defer c.Close()
	ctx := ctxTimeout(t)
	if _, err := c.Ping(ctx); err != nil {
		t.Fatal(err)
	}

	const size = 4096
	src := openSource(t, size)
	result := make(chan error, 1)
	go func() {
		result <- c.WriteImage(ctx, proto.WriteImageParams{Device: helpertest.Removable, Size: size}, src, nil)
	}()
	<-disk.Raw.Started
	// A dead helper is a closed socket, not a cancelled context: cancelling
	// the context races the op's own cancelled reply against the close.
	sc.Close()
	close(disk.Raw.Block)

	select {
	case err := <-result:
		if err == nil {
			t.Fatal("write succeeded after the helper died")
		}
		var pe *proto.Error
		if errors.As(err, &pe) {
			t.Fatalf("expected a transport error, got protocol error %v", pe)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("write hung after the helper died")
	}
	if _, err := c.Ping(ctx); err == nil {
		t.Fatal("ping succeeded on a dead connection")
	}
}

func TestClientSurfacesConnectionRefusals(t *testing.T) {
	ln := helpertest.NewPipeListener()
	defer ln.Close()
	srv := helper.New(helpertest.NewFakeDisk(), helpertest.FakeAuth{UID: os.Getuid()}, helper.Options{})
	go func() { _ = srv.Serve(context.Background(), ln) }()

	first := NewClient(ln.Dial())
	defer first.Close()
	if _, err := first.Ping(ctxTimeout(t)); err != nil {
		t.Fatal(err)
	}

	second := NewClient(ln.Dial())
	defer second.Close()
	_, err := second.Ping(ctxTimeout(t))
	wantCode(t, err, proto.CodeBusy)
	// Every later call on that client reports the same refusal.
	wantCode(t, second.Eject(ctxTimeout(t), helpertest.Removable), proto.CodeBusy)
}

func TestClientSurfacesUnauthorized(t *testing.T) {
	ln := helpertest.NewPipeListener()
	defer ln.Close()
	srv := helper.New(helpertest.NewFakeDisk(), helpertest.FakeAuth{Err: helper.ErrUnauthorized}, helper.Options{IdleTimeout: time.Second})
	go func() { _ = srv.Serve(context.Background(), ln) }()

	c := NewClient(ln.Dial())
	defer c.Close()
	_, err := c.Ping(ctxTimeout(t))
	wantCode(t, err, proto.CodeUnauthorized)
}

// The authorization token travels with the op and only with the ops that
// need it; the helper's authorizer sees it verbatim.
func TestClientAuthorizationPassesThrough(t *testing.T) {
	disk := helpertest.NewFakeDisk()
	az := &helpertest.FakeAuthorizer{Token: "c2VjcmV0"}
	srv := helper.New(disk, helpertest.FakeAuth{}, helper.Options{
		Version: "test", WriteBufferSize: 1024, ProgressInterval: time.Nanosecond, Authorizer: az,
	})
	cc, sc := helpertest.SocketPair(t)
	go func() { _ = srv.ServeConn(context.Background(), sc, helper.Peer{UID: os.Getuid()}) }()
	c := NewClient(cc)
	t.Cleanup(func() { c.Close() })
	ctx := ctxTimeout(t)
	if _, err := c.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	src := openSource(t, 4096)

	wantCode(t, c.WriteImage(ctx, proto.WriteImageParams{Device: helpertest.Removable, Size: 4096}, src, nil), proto.CodeUnauthorized)
	_, err := c.FormatDisk(ctx, proto.FormatDiskParams{Device: helpertest.Removable, Filesystem: "fat32", Label: "X", Authorization: "bm9wZQ=="})
	wantCode(t, err, proto.CodeUnauthorized)
	if disk.Raw != nil || len(disk.Formats) != 0 {
		t.Fatal("refused ops touched the disk")
	}

	if err := c.WriteImage(ctx, proto.WriteImageParams{Device: helpertest.Removable, Size: 4096, Authorization: "c2VjcmV0"}, src, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.FormatDisk(ctx, proto.FormatDiskParams{Device: helpertest.Removable, Filesystem: "fat32", Label: "X", Authorization: "c2VjcmV0"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Eject(ctx, helpertest.Removable); err != nil {
		t.Fatal(err)
	}
	if len(az.Calls) != 4 {
		t.Fatalf("authorizer saw %v, want two refusals and two grants", az.Calls)
	}
}
