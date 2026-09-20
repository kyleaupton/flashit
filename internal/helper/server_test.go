package helper_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kyleaupton/flashit/internal/helper"
	"github.com/kyleaupton/flashit/internal/helper/helpertest"
	"github.com/kyleaupton/flashit/internal/proto"
)

const testTimeout = 5 * time.Second

type client struct {
	t    *testing.T
	conn net.Conn
	r    *bufio.Reader
	done <-chan error
}

func testOptions() helper.Options {
	return helper.Options{Version: "test", WriteBufferSize: 1024, ProgressInterval: time.Nanosecond}
}

// serve starts ServeConn on one end of a pipe and returns a client on the other.
func serve(t *testing.T, disk helper.Disk, opts helper.Options) *client {
	t.Helper()
	srv := helper.New(disk, helpertest.FakeAuth{}, opts)
	cc, sc := net.Pipe()
	done := make(chan error, 1)
	go func() { done <- srv.ServeConn(context.Background(), sc, helper.Peer{UID: os.Getuid()}) }()
	_ = cc.SetDeadline(time.Now().Add(testTimeout))
	t.Cleanup(func() { cc.Close() })
	return &client{t: t, conn: cc, r: bufio.NewReader(cc), done: done}
}

func (c *client) send(id string, op proto.Op, params any) {
	c.t.Helper()
	req, err := proto.NewRequest(id, op, params)
	if err != nil {
		c.t.Fatal(err)
	}
	b, _ := json.Marshal(req)
	if _, err := c.conn.Write(append(b, '\n')); err != nil {
		c.t.Fatalf("send %s: %v", op, err)
	}
}

func (c *client) sendRaw(line string) {
	c.t.Helper()
	if _, err := c.conn.Write([]byte(line + "\n")); err != nil {
		c.t.Fatalf("send raw: %v", err)
	}
}

func (c *client) recv() proto.Response {
	c.t.Helper()
	line, err := c.r.ReadBytes('\n')
	if err != nil {
		c.t.Fatalf("recv: %v", err)
	}
	var resp proto.Response
	if err := json.Unmarshal(line, &resp); err != nil {
		c.t.Fatalf("decode %s: %v", line, err)
	}
	return resp
}

// call sends a request and reads until its result or error, collecting
// progress along the way.
func (c *client) call(id string, op proto.Op, params any) ([]proto.Response, proto.Response) {
	c.t.Helper()
	c.send(id, op, params)
	var progress []proto.Response
	for {
		resp := c.recv()
		if resp.ID != id {
			c.t.Fatalf("response for %q while waiting for %q: %+v", resp.ID, id, resp)
		}
		if resp.Type == proto.TypeProgress {
			progress = append(progress, resp)
			continue
		}
		return progress, resp
	}
}

func (c *client) ping() proto.PingResult {
	c.t.Helper()
	_, resp := c.call("ping", proto.OpPing, proto.PingParams{Protocol: proto.ProtocolVersion})
	if resp.Type != proto.TypeResult {
		c.t.Fatalf("ping failed: %+v", resp)
	}
	var r proto.PingResult
	if err := json.Unmarshal(resp.Data, &r); err != nil {
		c.t.Fatal(err)
	}
	return r
}

func (c *client) expectClosed() {
	c.t.Helper()
	if _, err := c.r.ReadBytes('\n'); err == nil {
		c.t.Fatal("expected the helper to close the connection")
	}
}

func (c *client) wait() error {
	c.t.Helper()
	select {
	case err := <-c.done:
		return err
	case <-time.After(testTimeout):
		c.t.Fatal("server did not exit")
		return nil
	}
}

func wantError(t *testing.T, resp proto.Response, code proto.ErrorCode) {
	t.Helper()
	if resp.Type != proto.TypeError {
		t.Fatalf("expected error %s, got %+v", code, resp)
	}
	if resp.Code != code {
		t.Fatalf("expected code %s, got %s (%s)", code, resp.Code, resp.Message)
	}
}

func wantResult(t *testing.T, resp proto.Response) {
	t.Helper()
	if resp.Type != proto.TypeResult {
		t.Fatalf("expected result, got %+v", resp)
	}
}

func sourceFile(t *testing.T, size int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "x.iso")
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPing(t *testing.T) {
	c := serve(t, helpertest.NewFakeDisk(), testOptions())
	r := c.ping()
	if r.Protocol != proto.ProtocolVersion || r.Version != "test" || r.EUID != os.Geteuid() {
		t.Fatalf("ping result %+v", r)
	}
	c.ping()
}

func TestVersionMismatchClosesConnection(t *testing.T) {
	c := serve(t, helpertest.NewFakeDisk(), testOptions())
	_, resp := c.call("1", proto.OpPing, proto.PingParams{Protocol: proto.ProtocolVersion + 1})
	wantError(t, resp, proto.CodeVersionMismatch)
	c.expectClosed()
	if err := c.wait(); err != nil {
		t.Fatalf("server returned %v", err)
	}
}

func TestOpBeforePingIsRefused(t *testing.T) {
	c := serve(t, helpertest.NewFakeDisk(), testOptions())
	_, resp := c.call("1", proto.OpEject, proto.EjectParams{Device: helpertest.Removable})
	wantError(t, resp, proto.CodeInvalidRequest)
	c.expectClosed()
}

func TestMalformedRequest(t *testing.T) {
	c := serve(t, helpertest.NewFakeDisk(), testOptions())
	c.ping()
	c.sendRaw(`{"id":"1","op":`)
	wantError(t, c.recv(), proto.CodeInvalidRequest)
	c.expectClosed()
}

func TestUnknownOpAndBadParams(t *testing.T) {
	c := serve(t, helpertest.NewFakeDisk(), testOptions())
	c.ping()
	_, resp := c.call("1", proto.Op("nuke"), nil)
	wantError(t, resp, proto.CodeInvalidRequest)
	c.sendRaw(`{"id":"2","op":"eject","params":"/dev/sdb"}`)
	wantError(t, c.recv(), proto.CodeInvalidRequest)
	_, resp = c.call("3", proto.OpMountISO, proto.MountISOParams{Path: "/tmp/x.iso"})
	wantError(t, resp, proto.CodeInvalidRequest)
	_, resp = c.call("4", proto.OpEject, nil)
	wantError(t, resp, proto.CodeInvalidDevice)
	c.ping()
}

func TestWriteImage(t *testing.T) {
	disk := helpertest.NewFakeDisk()
	c := serve(t, disk, testOptions())
	c.ping()

	const size = 10*1024 + 17
	src := sourceFile(t, size)
	progress, resp := c.call("1", proto.OpWriteImage, proto.WriteImageParams{
		Device: helpertest.RemovableLink, Source: src, Size: size,
	})
	wantResult(t, resp)

	want, _ := os.ReadFile(src)
	if got := disk.Raw.Bytes(); string(got) != string(want) {
		t.Fatalf("device holds %d bytes, want %d", len(got), len(want))
	}
	if disk.Raw.Device != helpertest.Removable {
		t.Fatalf("wrote to %s, expected the canonical %s", disk.Raw.Device, helpertest.Removable)
	}
	if disk.Raw.Syncs != 1 || disk.Raw.Closes == 0 {
		t.Fatalf("syncs=%d closes=%d", disk.Raw.Syncs, disk.Raw.Closes)
	}
	wantUnmounted := []string{helpertest.Removable + "1", helpertest.Removable + "2", helpertest.Removable}
	if len(disk.Unmounted) != len(wantUnmounted) {
		t.Fatalf("unmounted %v, want %v", disk.Unmounted, wantUnmounted)
	}
	for i := range wantUnmounted {
		if disk.Unmounted[i] != wantUnmounted[i] {
			t.Fatalf("unmounted %v, want %v", disk.Unmounted, wantUnmounted)
		}
	}
	if len(progress) == 0 {
		t.Fatal("no progress")
	}
	last := progress[len(progress)-1]
	if last.Written != size || last.Total != size {
		t.Fatalf("final progress %+v", last)
	}
	var prev uint64
	for _, p := range progress {
		if p.Written < prev || p.Total != size {
			t.Fatalf("progress went backwards: %+v", progress)
		}
		prev = p.Written
	}
}

func TestWriteImageRefusals(t *testing.T) {
	const size = 4096
	src := sourceFile(t, size)
	dir := t.TempDir()
	ok := proto.WriteImageParams{Device: helpertest.Removable, Source: src, Size: size}

	cases := []struct {
		name   string
		params proto.WriteImageParams
		setup  func(d *helpertest.FakeDisk)
		want   proto.ErrorCode
	}{
		{"path traversal", proto.WriteImageParams{Device: "/dev/../etc/shadow", Source: src, Size: size}, nil, proto.CodeInvalidDevice},
		{"outside /dev", proto.WriteImageParams{Device: "/etc/shadow", Source: src, Size: size}, nil, proto.CodeInvalidDevice},
		{"relative", proto.WriteImageParams{Device: "sdb", Source: src, Size: size}, nil, proto.CodeInvalidDevice},
		{"missing device", proto.WriteImageParams{Device: "/dev/sdz", Source: src, Size: size}, nil, proto.CodeInvalidDevice},
		{"not a block device", proto.WriteImageParams{Device: "/dev/null", Source: src, Size: size}, nil, proto.CodeInvalidDevice},
		{"partition", proto.WriteImageParams{Device: helpertest.Removable + "1", Source: src, Size: size}, nil, proto.CodeInvalidDevice},
		{"partition of system disk", proto.WriteImageParams{Device: helpertest.System + "p2", Source: src, Size: size}, nil, proto.CodeInvalidDevice},
		{"internal disk", proto.WriteImageParams{Device: helpertest.Internal, Source: src, Size: size}, nil, proto.CodeNotRemovable},
		{"system disk", proto.WriteImageParams{Device: helpertest.System, Source: src, Size: size}, nil, proto.CodeSystemDisk},
		{"system disk unknown", ok, func(d *helpertest.FakeDisk) { d.System = nil }, proto.CodeSystemDisk},
		{"system disk lookup fails", ok, func(d *helpertest.FakeDisk) { d.SystemErr = errors.New("no /proc") }, proto.CodeInternal},
		{"target became system disk", ok, func(d *helpertest.FakeDisk) { d.System = []string{helpertest.Removable} }, proto.CodeSystemDisk},
		{"missing source", proto.WriteImageParams{Device: helpertest.Removable, Source: filepath.Join(dir, "nope.iso"), Size: size}, nil, proto.CodeInvalidSource},
		{"relative source", proto.WriteImageParams{Device: helpertest.Removable, Source: "x.iso", Size: size}, nil, proto.CodeInvalidSource},
		{"directory source", proto.WriteImageParams{Device: helpertest.Removable, Source: dir, Size: size}, nil, proto.CodeInvalidSource},
		{"zero size", proto.WriteImageParams{Device: helpertest.Removable, Source: src, Size: 0}, nil, proto.CodeInvalidSource},
		{"size too small", proto.WriteImageParams{Device: helpertest.Removable, Source: src, Size: size - 1}, nil, proto.CodeSizeMismatch},
		{"size too large", proto.WriteImageParams{Device: helpertest.Removable, Source: src, Size: size + 1}, nil, proto.CodeSizeMismatch},
		{"device too small", ok, func(d *helpertest.FakeDisk) {
			info := d.Devices[helpertest.Removable]
			info.Size = size - 1
			d.Devices[helpertest.Removable] = info
		}, proto.CodeInsufficientCapacity},
		{"partition busy", ok, func(d *helpertest.FakeDisk) {
			d.UnmountErr = map[string]error{helpertest.Removable + "1": syscall.EBUSY}
		}, proto.CodeDeviceBusy},
		{"partitions unknown", ok, func(d *helpertest.FakeDisk) { d.PartitionsErr = errors.New("sysfs") }, proto.CodeInternal},
		{"device claimed", ok, func(d *helpertest.FakeDisk) { d.OpenRawErr = syscall.EBUSY }, proto.CodeDeviceBusy},
		{"open fails", ok, func(d *helpertest.FakeDisk) { d.OpenRawErr = errors.New("EIO") }, proto.CodeInternal},
		{"write fails", ok, func(d *helpertest.FakeDisk) { d.Raw = &helpertest.FakeRaw{WriteErr: errors.New("EIO")} }, proto.CodeInternal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			disk := helpertest.NewFakeDisk()
			if tc.setup != nil {
				tc.setup(disk)
			}
			c := serve(t, disk, testOptions())
			c.ping()
			_, resp := c.call("1", proto.OpWriteImage, tc.params)
			wantError(t, resp, tc.want)
			if disk.Raw != nil && tc.want != proto.CodeInternal && len(disk.Raw.Bytes()) > 0 {
				t.Fatalf("refused op wrote %d bytes", len(disk.Raw.Bytes()))
			}
			// The connection stays usable after a refused op.
			c.ping()
		})
	}
}

func TestWriteImageSourceShrinks(t *testing.T) {
	disk := helpertest.NewFakeDisk()
	c := serve(t, disk, testOptions())
	c.ping()

	const size = 8192
	src := sourceFile(t, size)
	disk.Raw = &helpertest.FakeRaw{Started: make(chan struct{}), Block: make(chan struct{})}
	c.send("1", proto.OpWriteImage, proto.WriteImageParams{Device: helpertest.Removable, Source: src, Size: size})
	<-disk.Raw.Started
	if err := os.Truncate(src, 2048); err != nil {
		t.Fatal(err)
	}
	close(disk.Raw.Block)
	for {
		resp := c.recv()
		if resp.Type == proto.TypeProgress {
			continue
		}
		wantError(t, resp, proto.CodeSizeMismatch)
		break
	}
}

func TestCancelMidWrite(t *testing.T) {
	disk := helpertest.NewFakeDisk()
	disk.Raw = &helpertest.FakeRaw{Started: make(chan struct{}), Block: make(chan struct{})}
	c := serve(t, disk, testOptions())
	c.ping()

	const size = 8192
	src := sourceFile(t, size)
	c.send("1", proto.OpWriteImage, proto.WriteImageParams{Device: helpertest.Removable, Source: src, Size: size})
	<-disk.Raw.Started

	c.send("2", proto.OpCancel, nil)
	resp := c.recv()
	if resp.ID != "2" || resp.Type != proto.TypeResult {
		t.Fatalf("cancel reply %+v", resp)
	}
	close(disk.Raw.Block)

	for {
		resp = c.recv()
		if resp.ID != "1" {
			t.Fatalf("unexpected %+v", resp)
		}
		if resp.Type == proto.TypeProgress {
			continue
		}
		wantError(t, resp, proto.CodeCancelled)
		break
	}
	if n := len(disk.Raw.Bytes()); n >= size {
		t.Fatalf("cancel did not stop the write: %d bytes", n)
	}
	if disk.Raw.Syncs != 0 {
		t.Fatal("a cancelled write must not report a sync")
	}
	// The helper is free again.
	c.ping()
	_, resp = c.call("3", proto.OpEject, proto.EjectParams{Device: helpertest.Removable})
	wantResult(t, resp)
}

func TestCancelWithNothingInFlight(t *testing.T) {
	c := serve(t, helpertest.NewFakeDisk(), testOptions())
	c.ping()
	_, resp := c.call("1", proto.OpCancel, nil)
	wantResult(t, resp)
}

func TestSecondOpWhileBusy(t *testing.T) {
	disk := helpertest.NewFakeDisk()
	disk.Raw = &helpertest.FakeRaw{Started: make(chan struct{}), Block: make(chan struct{})}
	c := serve(t, disk, testOptions())
	c.ping()

	const size = 4096
	src := sourceFile(t, size)
	c.send("1", proto.OpWriteImage, proto.WriteImageParams{Device: helpertest.Removable, Source: src, Size: size})
	<-disk.Raw.Started

	c.send("2", proto.OpEject, proto.EjectParams{Device: helpertest.Removable})
	resp := c.recv()
	if resp.ID != "2" {
		t.Fatalf("unexpected %+v", resp)
	}
	wantError(t, resp, proto.CodeBusy)
	if len(disk.Ejected) != 0 {
		t.Fatal("busy op ran anyway")
	}

	// ping is not an op and still answers.
	c.send("3", proto.OpPing, proto.PingParams{Protocol: proto.ProtocolVersion})
	resp = c.recv()
	if resp.ID != "3" || resp.Type != proto.TypeResult {
		t.Fatalf("ping while busy: %+v", resp)
	}

	close(disk.Raw.Block)
	for {
		resp = c.recv()
		if resp.Type == proto.TypeProgress {
			continue
		}
		if resp.ID != "1" {
			t.Fatalf("unexpected %+v", resp)
		}
		wantResult(t, resp)
		break
	}
	_, resp = c.call("4", proto.OpEject, proto.EjectParams{Device: helpertest.Removable})
	wantResult(t, resp)
}

func TestFormatDisk(t *testing.T) {
	disk := helpertest.NewFakeDisk()
	c := serve(t, disk, testOptions())
	c.ping()

	_, resp := c.call("1", proto.OpFormatDisk, proto.FormatDiskParams{Device: helpertest.Removable, Filesystem: "FAT32", Label: "FLASHIT"})
	wantResult(t, resp)
	var r proto.FormatDiskResult
	if err := json.Unmarshal(resp.Data, &r); err != nil || r.Mountpoint != disk.Mountpoint {
		t.Fatalf("result %s: %v", resp.Data, err)
	}
	if len(disk.Formats) != 1 || disk.Formats[0] != (helpertest.FormatCall{Device: helpertest.Removable, FS: "fat32", Label: "FLASHIT"}) {
		t.Fatalf("format calls %+v", disk.Formats)
	}
	if len(disk.Unmounted) != 3 {
		t.Fatalf("partitions were not unmounted first: %v", disk.Unmounted)
	}
}

func TestFormatDiskRefusals(t *testing.T) {
	cases := []struct {
		name   string
		params proto.FormatDiskParams
		setup  func(d *helpertest.FakeDisk)
		want   proto.ErrorCode
	}{
		{"path traversal", proto.FormatDiskParams{Device: "/dev/../etc/shadow", Filesystem: "fat32", Label: "X"}, nil, proto.CodeInvalidDevice},
		{"internal disk", proto.FormatDiskParams{Device: helpertest.Internal, Filesystem: "fat32", Label: "X"}, nil, proto.CodeNotRemovable},
		{"system disk", proto.FormatDiskParams{Device: helpertest.System, Filesystem: "fat32", Label: "X"}, nil, proto.CodeSystemDisk},
		{"ntfs", proto.FormatDiskParams{Device: helpertest.Removable, Filesystem: "ntfs", Label: "X"}, nil, proto.CodeInvalidRequest},
		{"empty label", proto.FormatDiskParams{Device: helpertest.Removable, Filesystem: "fat32", Label: ""}, nil, proto.CodeInvalidLabel},
		{"shell label", proto.FormatDiskParams{Device: helpertest.Removable, Filesystem: "fat32", Label: "$(id)"}, nil, proto.CodeInvalidLabel},
		{"long label", proto.FormatDiskParams{Device: helpertest.Removable, Filesystem: "fat32", Label: "ABCDEFGHIJKL"}, nil, proto.CodeInvalidLabel},
		{"busy partition", proto.FormatDiskParams{Device: helpertest.Removable, Filesystem: "fat32", Label: "X"}, func(d *helpertest.FakeDisk) {
			d.UnmountErr = map[string]error{helpertest.Removable + "2": syscall.EBUSY}
		}, proto.CodeDeviceBusy},
		{"format fails", proto.FormatDiskParams{Device: helpertest.Removable, Filesystem: "fat32", Label: "X"}, func(d *helpertest.FakeDisk) {
			d.FormatErr = errors.New("parted exit 1")
		}, proto.CodeInternal},
		{"mountpoint held", proto.FormatDiskParams{Device: helpertest.Removable, Filesystem: "fat32", Label: "X"}, func(d *helpertest.FakeDisk) {
			d.FormatErr = fmt.Errorf("unmount stale mount: %w", syscall.EBUSY)
		}, proto.CodeDeviceBusy},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			disk := helpertest.NewFakeDisk()
			if tc.setup != nil {
				tc.setup(disk)
			}
			c := serve(t, disk, testOptions())
			c.ping()
			_, resp := c.call("1", proto.OpFormatDisk, tc.params)
			wantError(t, resp, tc.want)
			if tc.want != proto.CodeInternal && tc.want != proto.CodeDeviceBusy && len(disk.Formats) != 0 {
				t.Fatalf("refused op formatted anyway: %+v", disk.Formats)
			}
		})
	}
}

func TestUnmount(t *testing.T) {
	disk := helpertest.NewFakeDisk()
	c := serve(t, disk, testOptions())
	c.ping()

	_, resp := c.call("1", proto.OpUnmount, proto.UnmountParams{Device: helpertest.Removable})
	wantResult(t, resp)
	if len(disk.Unmounted) != 3 {
		t.Fatalf("unmounted %v", disk.Unmounted)
	}

	disk.Unmounted = nil
	_, resp = c.call("2", proto.OpUnmount, proto.UnmountParams{Mountpoint: "/run/media/flashit/FLASHIT"})
	wantResult(t, resp)
	if len(disk.Unmounted) != 1 || disk.Unmounted[0] != "/run/media/flashit/FLASHIT" {
		t.Fatalf("unmounted %v", disk.Unmounted)
	}

	refusals := []struct {
		params proto.UnmountParams
		want   proto.ErrorCode
	}{
		{proto.UnmountParams{}, proto.CodeInvalidRequest},
		{proto.UnmountParams{Device: helpertest.Removable, Mountpoint: "/run/media/flashit/FLASHIT"}, proto.CodeInvalidRequest},
		{proto.UnmountParams{Mountpoint: "/"}, proto.CodeInvalidRequest},
		{proto.UnmountParams{Mountpoint: "/run/media/flashit/../../../boot"}, proto.CodeInvalidRequest},
		{proto.UnmountParams{Mountpoint: "/home/kyle"}, proto.CodeInvalidRequest},
		{proto.UnmountParams{Device: helpertest.System}, proto.CodeSystemDisk},
		{proto.UnmountParams{Device: helpertest.Internal}, proto.CodeNotRemovable},
		{proto.UnmountParams{Device: "/dev/../etc"}, proto.CodeInvalidDevice},
	}
	disk.Unmounted = nil
	for i, r := range refusals {
		_, resp := c.call("r", proto.OpUnmount, r.params)
		wantError(t, resp, r.want)
		if len(disk.Unmounted) != 0 {
			t.Fatalf("case %d unmounted %v", i, disk.Unmounted)
		}
	}

	disk.UnmountErr = map[string]error{"/run/media/flashit/FLASHIT": syscall.EBUSY}
	_, resp = c.call("3", proto.OpUnmount, proto.UnmountParams{Mountpoint: "/run/media/flashit/FLASHIT"})
	wantError(t, resp, proto.CodeDeviceBusy)
}

func TestEject(t *testing.T) {
	disk := helpertest.NewFakeDisk()
	c := serve(t, disk, testOptions())
	c.ping()

	_, resp := c.call("1", proto.OpEject, proto.EjectParams{Device: helpertest.RemovableLink})
	wantResult(t, resp)
	if len(disk.Ejected) != 1 || disk.Ejected[0] != helpertest.Removable {
		t.Fatalf("ejected %v", disk.Ejected)
	}

	for _, r := range []struct {
		device string
		want   proto.ErrorCode
	}{
		{"/dev/../etc/shadow", proto.CodeInvalidDevice},
		{helpertest.Removable + "1", proto.CodeInvalidDevice},
		{helpertest.Internal, proto.CodeNotRemovable},
		{helpertest.System, proto.CodeSystemDisk},
	} {
		_, resp := c.call("r", proto.OpEject, proto.EjectParams{Device: r.device})
		wantError(t, resp, r.want)
	}
	if len(disk.Ejected) != 1 {
		t.Fatalf("refused eject ran: %v", disk.Ejected)
	}

	disk.EjectErr = errors.New("no eject binary")
	_, resp = c.call("2", proto.OpEject, proto.EjectParams{Device: helpertest.Removable})
	wantError(t, resp, proto.CodeInternal)
}

func TestIdleExitWithoutRequests(t *testing.T) {
	opts := testOptions()
	opts.IdleTimeout = 50 * time.Millisecond
	c := serve(t, helpertest.NewFakeDisk(), opts)
	c.ping()
	if err := c.wait(); !errors.Is(err, helper.ErrIdle) {
		t.Fatalf("server returned %v, want ErrIdle", err)
	}
	c.expectClosed()
}

func TestIdleTimerPausesDuringOp(t *testing.T) {
	opts := testOptions()
	opts.IdleTimeout = 50 * time.Millisecond
	disk := helpertest.NewFakeDisk()
	disk.Raw = &helpertest.FakeRaw{Started: make(chan struct{}), Block: make(chan struct{})}
	c := serve(t, disk, opts)
	c.ping()

	const size = 4096
	src := sourceFile(t, size)
	c.send("1", proto.OpWriteImage, proto.WriteImageParams{Device: helpertest.Removable, Source: src, Size: size})
	<-disk.Raw.Started
	time.Sleep(3 * opts.IdleTimeout)
	close(disk.Raw.Block)
	for {
		resp := c.recv()
		if resp.Type == proto.TypeProgress {
			continue
		}
		wantResult(t, resp)
		break
	}
	if err := c.wait(); !errors.Is(err, helper.ErrIdle) {
		t.Fatalf("server returned %v, want ErrIdle after the op", err)
	}
}

func TestClientDisconnectEndsSession(t *testing.T) {
	c := serve(t, helpertest.NewFakeDisk(), testOptions())
	c.ping()
	c.conn.Close()
	if err := c.wait(); err != nil {
		t.Fatalf("server returned %v", err)
	}
}

func TestServeRefusesSecondConnection(t *testing.T) {
	ln := helpertest.NewPipeListener()
	defer ln.Close()
	srv := helper.New(helpertest.NewFakeDisk(), helpertest.FakeAuth{}, testOptions())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(context.Background(), ln) }()

	first := &client{t: t, conn: ln.Dial()}
	first.r = bufio.NewReader(first.conn)
	_ = first.conn.SetDeadline(time.Now().Add(testTimeout))
	first.ping()

	second := ln.Dial()
	_ = second.SetDeadline(time.Now().Add(testTimeout))
	line, err := bufio.NewReader(second).ReadBytes('\n')
	if err != nil {
		t.Fatalf("second connection: %v", err)
	}
	var resp proto.Response
	_ = json.Unmarshal(line, &resp)
	wantError(t, resp, proto.CodeBusy)
	if _, err := second.Read(make([]byte, 1)); err == nil {
		t.Fatal("second connection should be closed")
	}

	first.ping()
	first.conn.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned %v", err)
		}
	case <-time.After(testTimeout):
		t.Fatal("Serve did not return after the client left")
	}
}

func TestServeRejectsUnauthorized(t *testing.T) {
	ln := helpertest.NewPipeListener()
	defer ln.Close()
	opts := testOptions()
	opts.IdleTimeout = 200 * time.Millisecond
	srv := helper.New(helpertest.NewFakeDisk(), helpertest.FakeAuth{Err: helper.ErrUnauthorized}, opts)
	done := make(chan error, 1)
	go func() { done <- srv.Serve(context.Background(), ln) }()

	conn := ln.Dial()
	_ = conn.SetDeadline(time.Now().Add(testTimeout))
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var resp proto.Response
	_ = json.Unmarshal(line, &resp)
	wantError(t, resp, proto.CodeUnauthorized)
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("unauthorized connection should be closed")
	}

	// The slot was not consumed: the helper is still waiting, then exits idle.
	select {
	case err := <-done:
		if !errors.Is(err, helper.ErrIdle) {
			t.Fatalf("Serve returned %v, want ErrIdle", err)
		}
	case <-time.After(testTimeout):
		t.Fatal("Serve did not exit idle")
	}
}

func TestServeIdleWithoutConnection(t *testing.T) {
	ln := helpertest.NewPipeListener()
	defer ln.Close()
	opts := testOptions()
	opts.IdleTimeout = 50 * time.Millisecond
	srv := helper.New(helpertest.NewFakeDisk(), helpertest.FakeAuth{}, opts)
	if err := srv.Serve(context.Background(), ln); !errors.Is(err, helper.ErrIdle) {
		t.Fatalf("Serve returned %v, want ErrIdle", err)
	}
}

func TestServeStopsOnContext(t *testing.T) {
	ln := helpertest.NewPipeListener()
	defer ln.Close()
	srv := helper.New(helpertest.NewFakeDisk(), helpertest.FakeAuth{}, testOptions())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, ln) }()

	c := &client{t: t, conn: ln.Dial()}
	c.r = bufio.NewReader(c.conn)
	_ = c.conn.SetDeadline(time.Now().Add(testTimeout))
	c.ping()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Serve returned %v", err)
		}
	case <-time.After(testTimeout):
		t.Fatal("Serve ignored context cancellation")
	}
	c.expectClosed()
}

// The helper runs as root; a source the caller does not own must be refused
// before anything about it (including its size) is revealed.
func TestWriteImageRefusesSourceNotOwnedByCaller(t *testing.T) {
	disk := helpertest.NewFakeDisk()
	srv := helper.New(disk, helpertest.FakeAuth{}, testOptions())
	cc, sc := net.Pipe()
	go func() { _ = srv.ServeConn(context.Background(), sc, helper.Peer{UID: os.Getuid() + 1}) }()
	_ = cc.SetDeadline(time.Now().Add(testTimeout))
	t.Cleanup(func() { cc.Close() })
	c := &client{t: t, conn: cc, r: bufio.NewReader(cc)}
	c.ping()

	const size = 4096
	src := sourceFile(t, size)
	for _, claimed := range []int64{size, size - 1} {
		_, resp := c.call("1", proto.OpWriteImage, proto.WriteImageParams{Device: helpertest.Removable, Source: src, Size: claimed})
		wantError(t, resp, proto.CodeInvalidSource)
		if strings.Contains(resp.Message, strconv.Itoa(size)) {
			t.Fatalf("message leaks the size: %q", resp.Message)
		}
	}
	if disk.Raw != nil {
		t.Fatal("device was opened for a refused source")
	}
}

func TestWriteImageSizeMismatchHidesSize(t *testing.T) {
	c := serve(t, helpertest.NewFakeDisk(), testOptions())
	c.ping()
	const size = 4096
	src := sourceFile(t, size)
	_, resp := c.call("1", proto.OpWriteImage, proto.WriteImageParams{Device: helpertest.Removable, Source: src, Size: size + 1})
	wantError(t, resp, proto.CodeSizeMismatch)
	if strings.Contains(resp.Message, strconv.Itoa(size)) {
		t.Fatalf("message leaks the size: %q", resp.Message)
	}
}

// A finishing op must not re-arm the idle timer over the op that starts right
// after it: op B outlives the idle timeout several times over and must not be
// cut off.
func TestIdleTimerNotRearmedByPreviousOp(t *testing.T) {
	opts := testOptions()
	opts.IdleTimeout = 30 * time.Millisecond
	const size = 4096
	src := sourceFile(t, size)

	for i := 0; i < 20; i++ {
		disk := helpertest.NewFakeDisk()
		c := serve(t, disk, opts)
		c.ping()

		_, resp := c.call("a", proto.OpEject, proto.EjectParams{Device: helpertest.Removable})
		wantResult(t, resp)

		disk.Raw = &helpertest.FakeRaw{Started: make(chan struct{}), Block: make(chan struct{})}
		c.send("b", proto.OpWriteImage, proto.WriteImageParams{Device: helpertest.Removable, Source: src, Size: size})
		<-disk.Raw.Started
		time.Sleep(4 * opts.IdleTimeout)
		close(disk.Raw.Block)
		for {
			resp := c.recv()
			if resp.Type == proto.TypeProgress {
				continue
			}
			wantResult(t, resp)
			break
		}
		c.conn.Close()
		if err := c.wait(); err != nil {
			t.Fatalf("iteration %d: server returned %v", i, err)
		}
	}
}
