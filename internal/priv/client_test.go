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
	cc, sc := net.Pipe()
	done := make(chan error, 1)
	go func() { done <- srv.ServeConn(context.Background(), sc) }()
	c := NewClient(cc)
	t.Cleanup(func() { c.Close() })
	return c, done
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
	src := sourceFile(t, size)
	var calls int
	var last uint64
	err := c.WriteImage(ctx, helpertest.Removable, src, size, func(written, total uint64) {
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
	want, _ := os.ReadFile(src)
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
	src := sourceFile(t, 4096)

	wantCode(t, c.WriteImage(ctx, "/dev/../etc/shadow", src, 4096, nil), proto.CodeInvalidDevice)
	wantCode(t, c.WriteImage(ctx, helpertest.Internal, src, 4096, nil), proto.CodeNotRemovable)
	wantCode(t, c.WriteImage(ctx, helpertest.System, src, 4096, nil), proto.CodeSystemDisk)
	wantCode(t, c.WriteImage(ctx, helpertest.Removable, src, 4095, nil), proto.CodeSizeMismatch)
	wantCode(t, c.WriteImage(ctx, helpertest.Removable, filepath.Join(t.TempDir(), "no.iso"), 4096, nil), proto.CodeInvalidSource)
	wantCode(t, c.WriteImage(ctx, helpertest.Removable, src, helpertest.RemovableSize+1, nil), proto.CodeSizeMismatch)
	if disk.Raw != nil && len(disk.Raw.Bytes()) != 0 {
		t.Fatal("refused writes reached the device")
	}
	// Still connected and usable.
	if err := c.WriteImage(ctx, helpertest.Removable, src, 4096, nil); err != nil {
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

	mp, err := c.FormatDisk(ctx, helpertest.Removable, "FAT32", "FLASHIT")
	if err != nil {
		t.Fatal(err)
	}
	if mp != disk.Mountpoint {
		t.Fatalf("mountpoint %q", mp)
	}
	_, err = c.FormatDisk(ctx, helpertest.Removable, "FAT32", "bad;label")
	wantCode(t, err, proto.CodeInvalidLabel)
	_, err = c.FormatDisk(ctx, helpertest.Removable, "ntfs", "X")
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
	src := sourceFile(t, size)
	ctx, cancel := context.WithCancel(ctxTimeout(t))
	result := make(chan error, 1)
	go func() { result <- c.WriteImage(ctx, helpertest.Removable, src, size, nil) }()

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
	src := sourceFile(t, size)
	result := make(chan error, 1)
	go func() { result <- c.WriteImage(ctx, helpertest.Removable, src, size, nil) }()
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
	src := sourceFile(t, size)
	result := make(chan error, 1)
	go func() { result <- c.WriteImage(ctx, helpertest.Removable, src, size, nil) }()
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
	cc, sc := net.Pipe()
	serveCtx, stopServer := context.WithCancel(context.Background())
	go func() { _ = srv.ServeConn(serveCtx, sc) }()
	c := NewClient(cc)
	defer c.Close()
	ctx := ctxTimeout(t)
	if _, err := c.Ping(ctx); err != nil {
		t.Fatal(err)
	}

	const size = 4096
	src := sourceFile(t, size)
	result := make(chan error, 1)
	go func() { result <- c.WriteImage(ctx, helpertest.Removable, src, size, nil) }()
	<-disk.Raw.Started
	stopServer()
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
