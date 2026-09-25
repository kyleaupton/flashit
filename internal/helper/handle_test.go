package helper_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"testing"
	"time"

	"github.com/kyleaupton/flashit/internal/helper"
	"github.com/kyleaupton/flashit/internal/helper/helpertest"
	"github.com/kyleaupton/flashit/internal/proto"
)

// servePipe serves over net.Pipe, which carries no descriptors, like the
// Windows named pipe. It runs on every host.
func servePipe(t *testing.T, disk helper.Disk, opts helper.Options) *client {
	t.Helper()
	srv := helper.New(disk, helpertest.FakeAuth{}, opts)
	cc, sc := net.Pipe()
	done := make(chan error, 1)
	go func() { done <- srv.ServeConn(context.Background(), sc, helper.Peer{}) }()
	t.Cleanup(func() { cc.Close() })
	_ = cc.SetDeadline(time.Now().Add(testTimeout))
	return &client{t: t, conn: cc, r: bufio.NewReader(cc), done: done}
}

func TestWriteImageFromHandle(t *testing.T) {
	const size = 5000
	path := sourceFile(t, size)
	images := &helpertest.FakeImages{Files: map[uint64]string{0x1a4: path}}
	opts := testOptions()
	opts.Images = images
	disk := helpertest.NewFakeDisk()
	c := servePipe(t, disk, opts)
	c.ping()

	_, resp := c.call("1", proto.OpWriteImage, proto.WriteImageParams{Device: helpertest.Removable, Size: size, Handle: 0x1a4})
	wantResult(t, resp)
	want, _ := os.ReadFile(path)
	if !bytes.Equal(disk.Raw.Bytes(), want) {
		t.Fatal("device does not hold the image")
	}
	if len(images.Asked) != 1 || images.Asked[0] != 0x1a4 {
		t.Fatalf("asked for %v", images.Asked)
	}
	if err := images.Opened[0].Close(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("image left open: %v", err)
	}
}

// The helper reads the image by position: a handle duplicated out of the app
// shares the app's file offset, which may not be at zero.
func TestWriteImageIgnoresSharedOffset(t *testing.T) {
	const size = 3000
	path := sourceFile(t, size)
	f := openFile(t, path)
	if _, err := f.Seek(1234, 0); err != nil {
		t.Fatal(err)
	}
	opts := testOptions()
	opts.Images = imageFunc(func(uint64, int64) (*os.File, error) { return f, nil })
	disk := helpertest.NewFakeDisk()
	c := servePipe(t, disk, opts)
	c.ping()
	_, resp := c.call("1", proto.OpWriteImage, proto.WriteImageParams{Device: helpertest.Removable, Size: size, Handle: 7})
	wantResult(t, resp)
	want, _ := os.ReadFile(path)
	if !bytes.Equal(disk.Raw.Bytes(), want) {
		t.Fatal("device does not hold the image from its start")
	}
}

type imageFunc func(uint64, int64) (*os.File, error)

func (f imageFunc) Image(h uint64, size int64) (*os.File, error) { return f(h, size) }

func TestWriteImageHandleRefusals(t *testing.T) {
	path := sourceFile(t, 100)
	for _, tc := range []struct {
		name   string
		images helper.ImageSource
		handle uint64
		size   int64
		device string
		want   proto.ErrorCode
	}{
		{"no handle", &helpertest.FakeImages{Files: map[uint64]string{1: path}}, 0, 100, helpertest.Removable, proto.CodeInvalidRequest},
		{"no image source", nil, 1, 100, helpertest.Removable, proto.CodeInvalidRequest},
		{"handle not in the parent", &helpertest.FakeImages{}, 9, 100, helpertest.Removable, proto.CodeInvalidSource},
		{"source refuses with a code", &helpertest.FakeImages{Err: proto.NewError(proto.CodeInvalidSource, "a directory")}, 1, 100, helpertest.Removable, proto.CodeInvalidSource},
		{"size mismatch", &helpertest.FakeImages{Files: map[uint64]string{1: path}}, 1, 99, helpertest.Removable, proto.CodeSizeMismatch},
		{"system disk", &helpertest.FakeImages{Files: map[uint64]string{1: path}}, 1, 100, helpertest.System, proto.CodeSystemDisk},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := testOptions()
			opts.Images = tc.images
			disk := helpertest.NewFakeDisk()
			c := servePipe(t, disk, opts)
			c.ping()
			_, resp := c.call("1", proto.OpWriteImage, proto.WriteImageParams{Device: tc.device, Size: tc.size, Handle: tc.handle})
			wantError(t, resp, tc.want)
			if disk.Raw != nil {
				t.Fatal("device opened for a refused write")
			}
		})
	}
}

func TestServeExitOnReject(t *testing.T) {
	ln := helpertest.NewPipeListener()
	defer ln.Close()
	opts := testOptions()
	opts.ExitOnReject = true
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
	select {
	case err := <-done:
		if !errors.Is(err, helper.ErrUnauthorized) {
			t.Fatalf("Serve returned %v", err)
		}
	case <-time.After(testTimeout):
		t.Fatal("Serve kept listening after a rejected connection")
	}
}

func TestEjectUnmountsFirstWithoutMountTable(t *testing.T) {
	disk := helpertest.NewFakeDisk()
	c := servePipe(t, helpertest.UnmountFirst{Disk: disk}, testOptions())
	c.ping()
	_, resp := c.call("1", proto.OpEject, proto.EjectParams{Device: helpertest.Removable})
	wantResult(t, resp)
	log := disk.Snapshot()
	if len(log) == 0 || log[len(log)-1] != "eject "+helpertest.Removable || log[0] == "eject "+helpertest.Removable {
		t.Fatalf("log %v, want unmounts then eject", log)
	}

	disk = helpertest.NewFakeDisk()
	disk.UnmountErr = map[string]error{helpertest.Removable: errors.New("volume in use")}
	c = servePipe(t, helpertest.UnmountFirst{Disk: disk}, testOptions())
	c.ping()
	_, resp = c.call("1", proto.OpEject, proto.EjectParams{Device: helpertest.Removable})
	wantError(t, resp, proto.CodeDeviceBusy)
	if len(disk.Ejected) != 0 {
		t.Fatal("ejected a busy device")
	}

	disk = helpertest.NewFakeDisk()
	disk.EjectErr = errors.New("eject vetoed")
	c = servePipe(t, helpertest.UnmountFirst{Disk: disk}, testOptions())
	c.ping()
	_, resp = c.call("1", proto.OpEject, proto.EjectParams{Device: helpertest.Removable})
	wantResult(t, resp)
}
