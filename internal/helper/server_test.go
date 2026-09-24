package helper_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	"github.com/kyleaupton/flashit/internal/helper/validate"
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
	return helper.Options{Version: "test", WriteBufferSize: 1024, ProgressInterval: time.Nanosecond, UnmountRetry: time.Millisecond}
}

// serve starts ServeConn on one end of a unix socket pair and returns a
// client on the other, so requests can carry file descriptors.
func serve(t *testing.T, disk helper.Disk, opts helper.Options) *client {
	t.Helper()
	srv := helper.New(disk, helpertest.FakeAuth{}, opts)
	cc, sc := helpertest.SocketPair(t)
	done := make(chan error, 1)
	go func() { done <- srv.ServeConn(context.Background(), sc, helper.Peer{UID: os.Getuid()}) }()
	_ = cc.SetDeadline(time.Now().Add(testTimeout))
	return &client{t: t, conn: cc, r: bufio.NewReader(cc), done: done}
}

func (c *client) send(id string, op proto.Op, params any) {
	c.t.Helper()
	c.sendFDs(id, op, params, nil)
}

// sendFile sends a request with the file's descriptor attached, as the app
// does for write_image.
func (c *client) sendFile(id string, op proto.Op, params any, f *os.File) {
	c.t.Helper()
	c.sendFDs(id, op, params, []int{int(f.Fd())})
}

func (c *client) sendFDs(id string, op proto.Op, params any, fds []int) {
	c.t.Helper()
	req, err := proto.NewRequest(id, op, params)
	if err != nil {
		c.t.Fatal(err)
	}
	b, _ := json.Marshal(req)
	line := append(b, '\n')
	if len(fds) == 0 {
		if _, err := c.conn.Write(line); err != nil {
			c.t.Fatalf("send %s: %v", op, err)
		}
		return
	}
	if _, _, err := c.conn.(*helpertest.SyncConn).WriteMsgUnix(line, helpertest.Rights(fds...), nil); err != nil {
		c.t.Fatalf("send %s with fds: %v", op, err)
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
	return c.collect(id)
}

func (c *client) callFile(id string, op proto.Op, params any, f *os.File) ([]proto.Response, proto.Response) {
	c.t.Helper()
	c.sendFile(id, op, params, f)
	return c.collect(id)
}

// callFileAsync sends without collecting, for tests that interleave cancel.
func (c *client) callFileAsync(id string, op proto.Op, params any) {
	c.t.Helper()
	c.send(id, op, params)
}

func (c *client) collect(id string) ([]proto.Response, proto.Response) {
	c.t.Helper()
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

func openFile(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

// openSource is an open image of size bytes.
func openSource(t *testing.T, size int) *os.File {
	t.Helper()
	return openFile(t, sourceFile(t, size))
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
	path := sourceFile(t, size)
	progress, resp := c.callFile("1", proto.OpWriteImage, proto.WriteImageParams{
		Device: helpertest.RemovableLink, Size: size,
	}, openFile(t, path))
	wantResult(t, resp)

	want, _ := os.ReadFile(path)
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
	dir := t.TempDir()
	ok := proto.WriteImageParams{Device: helpertest.Removable, Size: size}
	regular := func(t *testing.T) *os.File { return openSource(t, size) }
	pipe := func(t *testing.T) *os.File {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { r.Close(); w.Close() })
		return r
	}
	directory := func(t *testing.T) *os.File { return openFile(t, dir) }
	none := func(*testing.T) *os.File { return nil }

	cases := []struct {
		name   string
		params proto.WriteImageParams
		image  func(*testing.T) *os.File
		setup  func(d *helpertest.FakeDisk)
		want   proto.ErrorCode
	}{
		{"path traversal", proto.WriteImageParams{Device: "/dev/../etc/shadow", Size: size}, regular, nil, proto.CodeInvalidDevice},
		{"outside /dev", proto.WriteImageParams{Device: "/etc/shadow", Size: size}, regular, nil, proto.CodeInvalidDevice},
		{"relative", proto.WriteImageParams{Device: "sdb", Size: size}, regular, nil, proto.CodeInvalidDevice},
		{"missing device", proto.WriteImageParams{Device: "/dev/sdz", Size: size}, regular, nil, proto.CodeInvalidDevice},
		{"not a block device", proto.WriteImageParams{Device: "/dev/null", Size: size}, regular, nil, proto.CodeInvalidDevice},
		{"partition", proto.WriteImageParams{Device: helpertest.Removable + "1", Size: size}, regular, nil, proto.CodeInvalidDevice},
		{"partition of system disk", proto.WriteImageParams{Device: helpertest.System + "p2", Size: size}, regular, nil, proto.CodeInvalidDevice},
		{"internal disk", proto.WriteImageParams{Device: helpertest.Internal, Size: size}, regular, nil, proto.CodeNotRemovable},
		{"system disk", proto.WriteImageParams{Device: helpertest.System, Size: size}, regular, nil, proto.CodeSystemDisk},
		{"system disk unknown", ok, regular, func(d *helpertest.FakeDisk) { d.System = nil }, proto.CodeSystemDisk},
		{"system disk lookup fails", ok, regular, func(d *helpertest.FakeDisk) { d.SystemErr = errors.New("no /proc") }, proto.CodeInternal},
		{"target became system disk", ok, regular, func(d *helpertest.FakeDisk) { d.System = []string{helpertest.Removable} }, proto.CodeSystemDisk},
		{"no image passed", ok, none, nil, proto.CodeInvalidRequest},
		{"pipe as image", ok, pipe, nil, proto.CodeInvalidSource},
		{"directory as image", ok, directory, nil, proto.CodeInvalidSource},
		{"zero size", proto.WriteImageParams{Device: helpertest.Removable, Size: 0}, regular, nil, proto.CodeInvalidSource},
		{"size too small", proto.WriteImageParams{Device: helpertest.Removable, Size: size - 1}, regular, nil, proto.CodeSizeMismatch},
		{"size too large", proto.WriteImageParams{Device: helpertest.Removable, Size: size + 1}, regular, nil, proto.CodeSizeMismatch},
		{"device too small", ok, regular, func(d *helpertest.FakeDisk) {
			info := d.Devices[helpertest.Removable]
			info.Size = size - 1
			d.Devices[helpertest.Removable] = info
		}, proto.CodeInsufficientCapacity},
		{"partition busy", ok, regular, func(d *helpertest.FakeDisk) {
			d.UnmountErr = map[string]error{helpertest.Removable + "1": syscall.EBUSY}
		}, proto.CodeDeviceBusy},
		{"partitions unknown", ok, regular, func(d *helpertest.FakeDisk) { d.PartitionsErr = errors.New("sysfs") }, proto.CodeInternal},
		{"device claimed", ok, regular, func(d *helpertest.FakeDisk) { d.OpenRawErr = syscall.EBUSY }, proto.CodeDeviceBusy},
		{"open fails", ok, regular, func(d *helpertest.FakeDisk) { d.OpenRawErr = errors.New("EIO") }, proto.CodeInternal},
		{"write fails", ok, regular, func(d *helpertest.FakeDisk) { d.Raw = &helpertest.FakeRaw{WriteErr: errors.New("EIO")} }, proto.CodeInternal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			disk := helpertest.NewFakeDisk()
			if tc.setup != nil {
				tc.setup(disk)
			}
			c := serve(t, disk, testOptions())
			c.ping()
			var resp proto.Response
			if f := tc.image(t); f != nil {
				_, resp = c.callFile("1", proto.OpWriteImage, tc.params, f)
			} else {
				_, resp = c.call("1", proto.OpWriteImage, tc.params)
			}
			wantError(t, resp, tc.want)
			if disk.Raw != nil && tc.want != proto.CodeInternal && len(disk.Raw.Bytes()) > 0 {
				t.Fatalf("refused op wrote %d bytes", len(disk.Raw.Bytes()))
			}
			// The connection stays usable after a refused op.
			c.ping()
		})
	}
}

// Only the first descriptor is the image; any others are closed at once,
// which shows as EOF on the pipe they were the last writer of.
func TestWriteImageClosesExtraDescriptors(t *testing.T) {
	disk := helpertest.NewFakeDisk()
	c := serve(t, disk, testOptions())
	c.ping()

	const size = 4096
	image := openSource(t, size)
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	c.sendFDs("1", proto.OpWriteImage, proto.WriteImageParams{Device: helpertest.Removable, Size: size}, []int{int(image.Fd()), int(pw.Fd())})
	pw.Close()
	_, resp := c.collect("1")
	wantResult(t, resp)
	if len(disk.Raw.Bytes()) != size {
		t.Fatalf("wrote %d bytes", len(disk.Raw.Bytes()))
	}
	_ = pr.SetReadDeadline(time.Now().Add(testTimeout))
	if _, err := pr.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("extra descriptor still open in the helper: %v", err)
	}

	// A descriptor sent with an op that takes none is closed too.
	pr2, pw2, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr2.Close()
	c.sendFDs("2", proto.OpEject, proto.EjectParams{Device: helpertest.Removable}, []int{int(pw2.Fd())})
	pw2.Close()
	_, resp = c.collect("2")
	wantResult(t, resp)
	_ = pr2.SetReadDeadline(time.Now().Add(testTimeout))
	if _, err := pr2.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("stray descriptor still open in the helper: %v", err)
	}
}

func TestWriteImageSourceShrinks(t *testing.T) {
	disk := helpertest.NewFakeDisk()
	c := serve(t, disk, testOptions())
	c.ping()

	const size = 8192
	path := sourceFile(t, size)
	disk.Raw = &helpertest.FakeRaw{Started: make(chan struct{}), Block: make(chan struct{})}
	c.sendFile("1", proto.OpWriteImage, proto.WriteImageParams{Device: helpertest.Removable, Size: size}, openFile(t, path))
	<-disk.Raw.Started
	if err := os.Truncate(path, 2048); err != nil {
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
	src := openSource(t, size)
	c.sendFile("1", proto.OpWriteImage, proto.WriteImageParams{Device: helpertest.Removable, Size: size}, src)
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
	src := openSource(t, size)
	c.sendFile("1", proto.OpWriteImage, proto.WriteImageParams{Device: helpertest.Removable, Size: size}, src)
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
	_, resp = c.call("2", proto.OpUnmount, proto.UnmountParams{Mountpoint: validate.MountRoot + "/FLASHIT"})
	wantResult(t, resp)
	if len(disk.Unmounted) != 1 || disk.Unmounted[0] != validate.MountRoot+"/FLASHIT" {
		t.Fatalf("unmounted %v", disk.Unmounted)
	}

	refusals := []struct {
		params proto.UnmountParams
		want   proto.ErrorCode
	}{
		{proto.UnmountParams{}, proto.CodeInvalidRequest},
		{proto.UnmountParams{Device: helpertest.Removable, Mountpoint: validate.MountRoot + "/FLASHIT"}, proto.CodeInvalidRequest},
		{proto.UnmountParams{Mountpoint: "/"}, proto.CodeInvalidRequest},
		{proto.UnmountParams{Mountpoint: validate.MountRoot + "/../../../boot"}, proto.CodeInvalidRequest},
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

	disk.UnmountErr = map[string]error{validate.MountRoot + "/FLASHIT": syscall.EBUSY}
	_, resp = c.call("3", proto.OpUnmount, proto.UnmountParams{Mountpoint: validate.MountRoot + "/FLASHIT"})
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

	disk.Log = nil
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
	if log := disk.Snapshot(); len(log) != 0 {
		t.Fatalf("refused eject touched the disk: %v", log)
	}
}

func TestEjectUnmountsFirst(t *testing.T) {
	disk := helpertest.NewFakeDisk()
	ours := validate.MountRoot + "/ESD-USB"
	disk.MountTab = map[string][]string{
		helpertest.Removable + "1": {ours, "/media/kyle/ESD-USB"},
		helpertest.Removable + "2": {
			validate.MountRoot,
			validate.MountRoot + "/a/b",
			validate.MountRoot + "/../../etc",
			validate.MountRoot + "/$(id)",
		},
		helpertest.Removable: {ours},
	}
	c := serve(t, disk, testOptions())
	c.ping()

	_, resp := c.call("1", proto.OpEject, proto.EjectParams{Device: helpertest.Removable})
	wantResult(t, resp)
	want := []string{
		"unmount " + helpertest.Removable + "1",
		"unmount " + helpertest.Removable + "2",
		"unmount " + helpertest.Removable,
		"rmdir " + ours,
		"eject " + helpertest.Removable,
	}
	if got := disk.Snapshot(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("calls %v, want %v", got, want)
	}
}

func TestEjectBusy(t *testing.T) {
	t.Run("gives up", func(t *testing.T) {
		disk := helpertest.NewFakeDisk()
		disk.BusyFor = map[string]int{helpertest.Removable + "1": 1000}
		disk.MountTab = map[string][]string{helpertest.Removable + "1": {validate.MountRoot + "/ESD-USB"}}
		c := serve(t, disk, testOptions())
		c.ping()

		_, resp := c.call("1", proto.OpEject, proto.EjectParams{Device: helpertest.Removable})
		wantError(t, resp, proto.CodeDeviceBusy)
		tries := 0
		for _, l := range disk.Snapshot() {
			switch {
			case l == "unmount "+helpertest.Removable+"1":
				tries++
			case strings.HasPrefix(l, "eject"), strings.HasPrefix(l, "rmdir"):
				t.Fatalf("%s ran while the volume was busy", l)
			}
		}
		if tries != 5 {
			t.Fatalf("tried to unmount %d times, want 5", tries)
		}
	})

	t.Run("released", func(t *testing.T) {
		disk := helpertest.NewFakeDisk()
		disk.BusyFor = map[string]int{helpertest.Removable + "1": 2}
		c := serve(t, disk, testOptions())
		c.ping()

		_, resp := c.call("1", proto.OpEject, proto.EjectParams{Device: helpertest.Removable})
		wantResult(t, resp)
		if len(disk.Ejected) != 1 {
			t.Fatalf("ejected %v", disk.Ejected)
		}
	})

	t.Run("other errors are not retried", func(t *testing.T) {
		disk := helpertest.NewFakeDisk()
		disk.UnmountErr = map[string]error{helpertest.Removable + "1": syscall.EPERM}
		c := serve(t, disk, testOptions())
		c.ping()

		_, resp := c.call("1", proto.OpEject, proto.EjectParams{Device: helpertest.Removable})
		wantError(t, resp, proto.CodeDeviceBusy)
		if log := disk.Snapshot(); len(log) != 1 {
			t.Fatalf("calls %v", log)
		}
	})
}

func TestEjectAfterUnmountIsBestEffort(t *testing.T) {
	disk := helpertest.NewFakeDisk()
	disk.EjectErr = errors.New("eject: unable to eject")
	disk.RemoveDirErr = errors.New("directory not empty")
	disk.MountTab = map[string][]string{helpertest.Removable + "1": {validate.MountRoot + "/ESD-USB"}}
	c := serve(t, disk, testOptions())
	c.ping()

	_, resp := c.call("1", proto.OpEject, proto.EjectParams{Device: helpertest.Removable})
	wantResult(t, resp)
	if len(disk.Unmounted) != 3 {
		t.Fatalf("unmounted %v", disk.Unmounted)
	}
}

// Without a MountTable (macOS) eject is the OS eject alone, and its failure
// is the op's.
func TestEjectWithoutMountTable(t *testing.T) {
	disk := helpertest.NewFakeDisk()
	disk.EjectErr = errors.New("diskutil eject failed")
	c := serve(t, helpertest.NoMountTable{Disk: disk}, testOptions())
	c.ping()

	_, resp := c.call("1", proto.OpEject, proto.EjectParams{Device: helpertest.Removable})
	wantError(t, resp, proto.CodeInternal)
	if log := disk.Snapshot(); len(log) != 1 || log[0] != "eject "+helpertest.Removable {
		t.Fatalf("calls %v", log)
	}
}

func TestUnmountDeviceRetriesAndRemovesDirs(t *testing.T) {
	disk := helpertest.NewFakeDisk()
	disk.BusyFor = map[string]int{helpertest.Removable + "1": 3}
	disk.MountTab = map[string][]string{helpertest.Removable + "1": {validate.MountRoot + "/ESD-USB"}}
	c := serve(t, disk, testOptions())
	c.ping()

	_, resp := c.call("1", proto.OpUnmount, proto.UnmountParams{Device: helpertest.Removable})
	wantResult(t, resp)
	if len(disk.RemovedDirs) != 1 || disk.RemovedDirs[0] != validate.MountRoot+"/ESD-USB" {
		t.Fatalf("removed %v", disk.RemovedDirs)
	}
	if len(disk.Ejected) != 0 {
		t.Fatal("unmount ejected")
	}

	disk.BusyFor = map[string]int{helpertest.Removable + "1": 1000}
	_, resp = c.call("2", proto.OpUnmount, proto.UnmountParams{Device: helpertest.Removable})
	wantError(t, resp, proto.CodeDeviceBusy)
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
	src := openSource(t, size)
	c.sendFile("1", proto.OpWriteImage, proto.WriteImageParams{Device: helpertest.Removable, Size: size}, src)
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

func TestWriteImageSizeMismatchHidesSize(t *testing.T) {
	c := serve(t, helpertest.NewFakeDisk(), testOptions())
	c.ping()
	const size = 4096
	_, resp := c.callFile("1", proto.OpWriteImage, proto.WriteImageParams{Device: helpertest.Removable, Size: size + 1}, openSource(t, size))
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
	path := sourceFile(t, size)

	for i := 0; i < 20; i++ {
		disk := helpertest.NewFakeDisk()
		c := serve(t, disk, opts)
		c.ping()

		_, resp := c.call("a", proto.OpEject, proto.EjectParams{Device: helpertest.Removable})
		wantResult(t, resp)

		disk.Raw = &helpertest.FakeRaw{Started: make(chan struct{}), Block: make(chan struct{})}
		// The helper reads through the passed descriptor's own offset, so
		// each iteration needs a fresh open.
		c.sendFile("b", proto.OpWriteImage, proto.WriteImageParams{Device: helpertest.Removable, Size: size}, openFile(t, path))
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

func TestAuthorizerGatesDestructiveOps(t *testing.T) {
	const size = 4096
	write := func(token string) proto.WriteImageParams {
		return proto.WriteImageParams{Device: helpertest.Removable, Size: size, Authorization: token}
	}
	format := func(token string) proto.FormatDiskParams {
		return proto.FormatDiskParams{Device: helpertest.Removable, Filesystem: "fat32", Label: "X", Authorization: token}
	}

	t.Run("missing token", func(t *testing.T) {
		disk := helpertest.NewFakeDisk()
		az := &helpertest.FakeAuthorizer{Token: "good"}
		opts := testOptions()
		opts.Authorizer = az
		c := serve(t, disk, opts)
		c.ping()
		_, resp := c.callFile("1", proto.OpWriteImage, write(""), openSource(t, size))
		wantError(t, resp, proto.CodeUnauthorized)
		_, resp = c.call("2", proto.OpFormatDisk, format(""))
		wantError(t, resp, proto.CodeUnauthorized)
		if disk.Raw != nil || len(disk.Formats) != 0 || len(disk.Unmounted) != 0 {
			t.Fatalf("refused op touched the disk: raw=%v formats=%v unmounted=%v", disk.Raw, disk.Formats, disk.Unmounted)
		}
		if az.CallCount() != 2 {
			t.Fatalf("authorizer called %d times", az.CallCount())
		}
	})

	t.Run("wrong token", func(t *testing.T) {
		disk := helpertest.NewFakeDisk()
		opts := testOptions()
		opts.Authorizer = &helpertest.FakeAuthorizer{Token: "good"}
		c := serve(t, disk, opts)
		c.ping()
		_, resp := c.callFile("1", proto.OpWriteImage, write("forged"), openSource(t, size))
		wantError(t, resp, proto.CodeUnauthorized)
		if strings.Contains(resp.Message, "forged") || strings.Contains(resp.Message, "good") {
			t.Fatalf("message leaks the token: %q", resp.Message)
		}
		_, resp = c.call("2", proto.OpFormatDisk, format("forged"))
		wantError(t, resp, proto.CodeUnauthorized)
		if disk.Raw != nil || len(disk.Formats) != 0 || len(disk.Unmounted) != 0 {
			t.Fatal("refused op touched the disk")
		}
		// The session is still usable, and the token is required per op.
		_, resp = c.callFile("3", proto.OpWriteImage, write("good"), openSource(t, size))
		wantResult(t, resp)
		_, resp = c.callFile("4", proto.OpWriteImage, write("forged"), openSource(t, size))
		wantError(t, resp, proto.CodeUnauthorized)
	})

	t.Run("validation runs before the prompt", func(t *testing.T) {
		az := &helpertest.FakeAuthorizer{Token: "good"}
		opts := testOptions()
		opts.Authorizer = az
		c := serve(t, helpertest.NewFakeDisk(), opts)
		c.ping()
		for _, p := range []proto.WriteImageParams{
			{Device: helpertest.System, Size: size, Authorization: "good"},
			{Device: helpertest.Internal, Size: size, Authorization: "good"},
			{Device: helpertest.Removable, Size: size + 1, Authorization: "good"},
		} {
			_, resp := c.callFile("1", proto.OpWriteImage, p, openSource(t, size))
			if resp.Type != proto.TypeError || resp.Code == proto.CodeUnauthorized {
				t.Fatalf("expected a validation error, got %+v", resp)
			}
		}
		_, resp := c.call("2", proto.OpFormatDisk, proto.FormatDiskParams{Device: helpertest.Removable, Filesystem: "fat32", Label: "$(id)", Authorization: "good"})
		wantError(t, resp, proto.CodeInvalidLabel)
		if az.CallCount() != 0 {
			t.Fatalf("authorizer prompted %d times for requests that fail validation", az.CallCount())
		}
	})

	t.Run("token passes through", func(t *testing.T) {
		disk := helpertest.NewFakeDisk()
		az := &helpertest.FakeAuthorizer{Token: "good"}
		opts := testOptions()
		opts.Authorizer = az
		c := serve(t, disk, opts)
		c.ping()
		_, resp := c.call("1", proto.OpFormatDisk, format("good"))
		wantResult(t, resp)
		_, resp = c.call("2", proto.OpEject, proto.EjectParams{Device: helpertest.Removable})
		wantResult(t, resp)
		_, resp = c.call("3", proto.OpUnmount, proto.UnmountParams{Device: helpertest.Removable})
		wantResult(t, resp)
		if len(az.Calls) != 1 || az.Calls[0] != proto.OpFormatDisk {
			t.Fatalf("authorizer calls %v, want only format_disk", az.Calls)
		}
		if len(az.Grants) != 1 || !az.Grants[0].WasReleased() {
			t.Fatal("format_disk did not release its grant")
		}
	})

	t.Run("refusal codes pass through", func(t *testing.T) {
		for _, tc := range []struct {
			err  error
			want proto.ErrorCode
		}{
			{proto.NewError(proto.CodeTCCDenied, "removable volumes refused"), proto.CodeTCCDenied},
			{proto.NewError(proto.CodeCancelled, "sheet cancelled"), proto.CodeCancelled},
			{errors.New("OSStatus -60008"), proto.CodeUnauthorized},
		} {
			disk := helpertest.NewFakeDisk()
			opts := testOptions()
			opts.Authorizer = &helpertest.FakeAuthorizer{Token: "good", Err: tc.err}
			c := serve(t, disk, opts)
			c.ping()
			_, resp := c.callFile("1", proto.OpWriteImage, write("good"), openSource(t, size))
			wantError(t, resp, tc.want)
			if tc.want == proto.CodeUnauthorized && strings.Contains(resp.Message, "60008") {
				t.Fatalf("message leaks the reason: %q", resp.Message)
			}
			if len(disk.OpenRawGrants) != 0 || len(disk.Unmounted) != 0 {
				t.Fatal("refused op touched the disk")
			}
		}
	})
}

// The grant Authorize returns is what OpenRaw receives, for the device that
// was resolved, and it is released as soon as OpenRaw has returned, whether
// the open, the unmount before it or the write after it failed.
func TestGrantLifetime(t *testing.T) {
	const size = 4096
	params := proto.WriteImageParams{Device: helpertest.RemovableLink, Size: size, Authorization: "good"}
	cases := []struct {
		name     string
		setup    func(d *helpertest.FakeDisk)
		want     proto.ErrorCode
		openRaws int
	}{
		{"write succeeds", nil, "", 1},
		{"open fails", func(d *helpertest.FakeDisk) { d.OpenRawErr = errors.New("EIO") }, proto.CodeInternal, 1},
		{"open reports a code", func(d *helpertest.FakeDisk) { d.OpenRawErr = proto.NewError(proto.CodeTCCDenied, "denied late") }, proto.CodeTCCDenied, 1},
		{"unmount fails", func(d *helpertest.FakeDisk) {
			d.UnmountErr = map[string]error{helpertest.Removable + "1": syscall.EBUSY}
		}, proto.CodeDeviceBusy, 0},
		{"write fails", func(d *helpertest.FakeDisk) { d.Raw = &helpertest.FakeRaw{WriteErr: errors.New("EIO")} }, proto.CodeInternal, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			disk := helpertest.NewFakeDisk()
			if tc.setup != nil {
				tc.setup(disk)
			}
			az := &helpertest.FakeAuthorizer{Token: "good"}
			opts := testOptions()
			opts.Authorizer = az
			c := serve(t, disk, opts)
			c.ping()
			_, resp := c.callFile("1", proto.OpWriteImage, params, openSource(t, size))
			if tc.want == "" {
				wantResult(t, resp)
			} else {
				wantError(t, resp, tc.want)
			}
			if len(az.Devices) != 1 || az.Devices[0] != helpertest.Removable {
				t.Fatalf("authorizer saw devices %v, want the resolved %s", az.Devices, helpertest.Removable)
			}
			if len(disk.OpenRawGrants) != tc.openRaws {
				t.Fatalf("OpenRaw called %d times, want %d", len(disk.OpenRawGrants), tc.openRaws)
			}
			if tc.openRaws == 1 && disk.OpenRawGrants[0] != az.Grants[0] {
				t.Fatal("OpenRaw did not receive the grant Authorize returned")
			}
			if !az.Grants[0].WasReleased() {
				t.Fatal("grant was not released")
			}
		})
	}
}

// A cancel that arrives while the sheet is up must win over the answer,
// and nothing on the disk may be touched for an op cancelled that way.
func TestCancelDuringAuthorization(t *testing.T) {
	for _, approve := range []bool{true, false} {
		disk := helpertest.NewFakeDisk()
		az := &helpertest.FakeAuthorizer{Token: "good", Block: make(chan struct{}), Started: make(chan struct{})}
		opts := testOptions()
		opts.Authorizer = az
		c := serve(t, disk, opts)
		c.ping()

		token := "good"
		if !approve {
			token = "forged"
		}
		c.callFileAsync("1", proto.OpFormatDisk, proto.FormatDiskParams{Device: helpertest.Removable, Filesystem: "fat32", Label: "X", Authorization: token})
		<-az.Started
		c.send("2", proto.OpCancel, nil)
		if resp := c.recv(); resp.ID != "2" || resp.Type != proto.TypeResult {
			t.Fatalf("cancel reply %+v", resp)
		}
		close(az.Block)
		_, resp := c.collect("1")
		wantError(t, resp, proto.CodeCancelled)
		if len(disk.Unmounted) != 0 || len(disk.Formats) != 0 {
			t.Fatalf("approve=%v: cancelled op touched the disk: unmounted=%v formats=%v", approve, disk.Unmounted, disk.Formats)
		}
		if approve && (len(az.Grants) != 1 || !az.Grants[0].WasReleased()) {
			t.Fatalf("grant of a cancelled op was not released: %+v", az.Grants)
		}
		c.ping()
	}
}
