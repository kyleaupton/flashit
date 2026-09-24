// Package helpertest holds fakes for the helper's OS interfaces so the server
// and the app-side client can be tested together on any platform.
package helpertest

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"sync"
	"syscall"

	"github.com/kyleaupton/flashit/internal/helper"
	"github.com/kyleaupton/flashit/internal/helper/validate"
	"github.com/kyleaupton/flashit/internal/proto"
)

const (
	// Removable is a 1 MiB removable whole disk with two partitions.
	Removable = "/dev/sdb"
	// RemovableLink is a symlink that Stat resolves to Removable.
	RemovableLink = "/dev/disk/by-id/usb-Fake"
	// Internal is a non-removable whole disk.
	Internal = "/dev/sda"
	// System is the whole disk backing "/".
	System = "/dev/nvme0n1"
	// RemovableSize is the size of Removable in bytes.
	RemovableSize = 1 << 20
)

type FormatCall struct {
	Device, FS, Label string
}

// FakeDisk is an in-memory Disk. Zero values behave sensibly; NewFakeDisk
// preloads a typical machine.
type FakeDisk struct {
	mu sync.Mutex

	Devices       map[string]helper.DeviceInfo
	System        []string
	SystemErr     error
	Parts         map[string][]string
	PartitionsErr error
	UnmountErr    map[string]error
	// BusyFor makes Unmount of a path fail with EBUSY this many times
	// before it succeeds.
	BusyFor    map[string]int
	Unmounted  []string
	OpenRawErr error
	Raw        *FakeRaw
	// OpenRawGrants records the grant passed to every OpenRaw call.
	OpenRawGrants []helper.Grant
	FormatErr     error
	Mountpoint    string
	Formats       []FormatCall
	EjectErr      error
	Ejected       []string
	// MountTab is what Mounts reports per device; RemovedDirs records every
	// RemoveMountDir that succeeded.
	MountTab     map[string][]string
	MountsErr    error
	RemoveDirErr error
	RemovedDirs  []string
	// Log is every Unmount, Eject and RemoveMountDir call in order, as
	// "unmount <path>", "eject <device>" and "rmdir <dir>".
	Log []string
}

func NewFakeDisk() *FakeDisk {
	removable := helper.DeviceInfo{Path: Removable, IsBlock: true, WholeDisk: Removable, Size: RemovableSize, Removable: true}
	return &FakeDisk{
		Devices: map[string]helper.DeviceInfo{
			Removable:       removable,
			RemovableLink:   removable,
			Removable + "1": {Path: Removable + "1", IsBlock: true, WholeDisk: Removable, Size: RemovableSize / 2, Removable: true},
			Internal:        {Path: Internal, IsBlock: true, WholeDisk: Internal, Size: 1 << 40, Removable: false},
			System:          {Path: System, IsBlock: true, WholeDisk: System, Size: 1 << 40, Removable: true},
			System + "p2":   {Path: System + "p2", IsBlock: true, WholeDisk: System, Size: 1 << 39, Removable: true},
			"/dev/null":     {Path: "/dev/null", IsBlock: false, WholeDisk: "/dev/null", Removable: true},
		},
		System: []string{System},
		Parts: map[string][]string{
			Removable: {Removable + "1", Removable + "2"},
		},
		Mountpoint: validate.MountRoot + "/FLASHIT",
	}
}

func (d *FakeDisk) Stat(path string) (helper.DeviceInfo, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	info, ok := d.Devices[path]
	if !ok {
		return helper.DeviceInfo{}, fmt.Errorf("no such device %s", path)
	}
	return info, nil
}

func (d *FakeDisk) SystemDisks() ([]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.System, d.SystemErr
}

func (d *FakeDisk) Partitions(device string) ([]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.Parts[device], d.PartitionsErr
}

func (d *FakeDisk) Unmount(path string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.Log = append(d.Log, "unmount "+path)
	if d.BusyFor[path] > 0 {
		d.BusyFor[path]--
		return fmt.Errorf("umount %s: %w", path, syscall.EBUSY)
	}
	if err := d.UnmountErr[path]; err != nil {
		return err
	}
	d.Unmounted = append(d.Unmounted, path)
	return nil
}

func (d *FakeDisk) OpenRaw(device string, grant helper.Grant) (helper.RawDevice, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.OpenRawGrants = append(d.OpenRawGrants, grant)
	if d.OpenRawErr != nil {
		return nil, d.OpenRawErr
	}
	if d.Raw == nil {
		d.Raw = &FakeRaw{}
	}
	d.Raw.Device = device
	return d.Raw, nil
}

func (d *FakeDisk) Format(ctx context.Context, device, fs, label string) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.Formats = append(d.Formats, FormatCall{device, fs, label})
	if d.FormatErr != nil {
		return "", d.FormatErr
	}
	return d.Mountpoint, nil
}

func (d *FakeDisk) Eject(device string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.Log = append(d.Log, "eject "+device)
	if d.EjectErr != nil {
		return d.EjectErr
	}
	d.Ejected = append(d.Ejected, device)
	return nil
}

func (d *FakeDisk) Mounts(device string) ([]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.MountTab[device], d.MountsErr
}

func (d *FakeDisk) RemoveMountDir(dir string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.Log = append(d.Log, "rmdir "+dir)
	if d.RemoveDirErr != nil {
		return d.RemoveDirErr
	}
	d.RemovedDirs = append(d.RemovedDirs, dir)
	return nil
}

// Snapshot returns a copy of Log.
func (d *FakeDisk) Snapshot() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.Log...)
}

// NoMountTable hides FakeDisk's MountTable methods, so the helper treats it
// like a host whose eject unmounts on its own (macOS).
type NoMountTable struct {
	helper.Disk
}

// FakeRaw records what was written to a device. Block, when set, makes every
// Write wait until it is closed; Started is closed on the first Write.
type FakeRaw struct {
	mu       sync.Mutex
	Device   string
	Buf      bytes.Buffer
	Syncs    int
	Closes   int
	WriteErr error
	Block    chan struct{}
	Started  chan struct{}
	started  sync.Once
}

func (r *FakeRaw) Write(p []byte) (int, error) {
	if r.Started != nil {
		r.started.Do(func() { close(r.Started) })
	}
	if r.Block != nil {
		<-r.Block
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.WriteErr != nil {
		return 0, r.WriteErr
	}
	return r.Buf.Write(p)
}

func (r *FakeRaw) Sync() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Syncs++
	return nil
}

func (r *FakeRaw) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Closes++
	return nil
}

func (r *FakeRaw) Bytes() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return bytes.Clone(r.Buf.Bytes())
}

// FakeAuth accepts every connection as UID unless Err is set.
type FakeAuth struct {
	Err error
	UID int
}

func (a FakeAuth) Authenticate(net.Conn) (helper.Peer, error) {
	if a.Err != nil {
		return helper.Peer{}, a.Err
	}
	return helper.Peer{UID: a.UID}, nil
}

// FakeAuthorizer accepts exactly Token and records every call. Err, when
// set, is returned for every call instead. Block, when set, makes every
// call wait until it is closed, like a sheet nobody has answered; Started
// is closed on the first call.
type FakeAuthorizer struct {
	mu      sync.Mutex
	Token   string
	Err     error
	Calls   []proto.Op
	Devices []string
	Grants  []*FakeGrant
	Block   chan struct{}
	Started chan struct{}
	started sync.Once
}

// FakeGrant records whether it was released.
type FakeGrant struct {
	mu       sync.Mutex
	Released bool
}

func (g *FakeGrant) Release() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.Released = true
}

func (g *FakeGrant) WasReleased() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.Released
}

func (a *FakeAuthorizer) Authorize(op proto.Op, token string, device helper.DeviceInfo) (helper.Grant, error) {
	if a.Started != nil {
		a.started.Do(func() { close(a.Started) })
	}
	if a.Block != nil {
		<-a.Block
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.Calls = append(a.Calls, op)
	a.Devices = append(a.Devices, device.Path)
	switch {
	case a.Err != nil:
		return nil, a.Err
	case token == "":
		return nil, fmt.Errorf("no authorization for %s", op)
	case token != a.Token:
		return nil, fmt.Errorf("authorization for %s was refused", op)
	}
	g := &FakeGrant{}
	a.Grants = append(a.Grants, g)
	return g, nil
}

func (a *FakeAuthorizer) CallCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.Calls)
}

// PipeListener is a net.Listener over net.Pipe for tests of the accept loop.
type PipeListener struct {
	conns chan net.Conn
	done  chan struct{}
	once  sync.Once
}

func NewPipeListener() *PipeListener {
	return &PipeListener{conns: make(chan net.Conn), done: make(chan struct{})}
}

func (l *PipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *PipeListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return nil
}

func (l *PipeListener) Addr() net.Addr { return pipeAddr{} }

// Dial hands the server side of a new pipe to Accept and returns the client
// side.
func (l *PipeListener) Dial() net.Conn {
	client, server := net.Pipe()
	select {
	case l.conns <- server:
	case <-l.done:
		client.Close()
	}
	return client
}

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "pipe" }
