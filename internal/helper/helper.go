// Package helper is the privileged side of FlashIt: NDJSON over one
// connection, one op at a time, every op validated before the OS is touched.
// The OS sits behind Disk, Auth and Authorizer so the whole package tests on
// any platform with fakes.
package helper

import (
	"context"
	"errors"
	"io"
	"net"

	"github.com/kyleaupton/flashit/internal/helper/validate"
	"github.com/kyleaupton/flashit/internal/proto"
)

type DeviceInfo = validate.DeviceInfo

// RawDevice is an exclusively opened block device.
type RawDevice interface {
	io.Writer
	io.Closer
	Sync() error
}

// Disk is what the helper needs from the OS. Every path it receives has
// already passed validate.DevicePath; every path it returns must be canonical.
type Disk interface {
	// Stat resolves symlinks and reports what the path is. It must fail for
	// anything that does not exist.
	Stat(path string) (DeviceInfo, error)
	// SystemDisks returns the whole disks backing the running system. An
	// error or an empty list refuses every destructive op.
	SystemDisks() ([]string, error)
	// Partitions lists the partition device paths of a whole disk.
	Partitions(device string) ([]string, error)
	// Unmount takes a device path (whole disk or partition) or a mountpoint
	// and unmounts everything mounted from or at it. It returns
	// syscall.EBUSY when something holds the mount.
	Unmount(path string) error
	// OpenRaw opens the device for exclusive raw writing. grant is what the
	// Authorizer returned for this device; a host that needs no per-op
	// authorization gets NopGrant. It returns syscall.EBUSY when something
	// else holds the device.
	OpenRaw(device string, grant Grant) (RawDevice, error)
	// Format writes an MBR with one active fat32 partition, formats it with
	// label, mounts it under validate.MountRoot and returns the mountpoint.
	Format(ctx context.Context, device, fs, label string) (string, error)
	Eject(device string) error
}

// MountTable is implemented by a Disk whose Eject does not unmount on its
// own (Linux). eject then unmounts every partition itself, removes the
// directories format_disk mounted them on, and treats Eject as best effort,
// since the device is safe to pull once nothing is mounted.
type MountTable interface {
	// Mounts lists where device, a whole disk or a partition, is mounted,
	// as the OS mount table has it.
	Mounts(device string) ([]string, error)
	// RemoveMountDir removes dir only if it is an empty, unmounted, real
	// directory directly under validate.MountRoot.
	RemoveMountDir(dir string) error
}

// Peer is what caller authentication learned about the connected process.
// Ops use it to refuse sources the caller could not read on their own.
type Peer struct {
	UID int
}

// Auth decides whether the process on the other end of conn is the app.
type Auth interface {
	Authenticate(conn net.Conn) (Peer, error)
}

// Authorizer decides whether the user approved a destructive op on device.
// token is the request's authorization field, verbatim; the authorizer is
// free to prompt. It runs after every other check has passed so a refused
// request never costs the user a prompt. A *proto.Error reaches the client
// with its code; any other error is reported as unauthorized.
type Authorizer interface {
	Authorize(op proto.Op, token string, device DeviceInfo) (Grant, error)
}

// Grant is the approval an Authorizer hands back, and on macOS the
// authorized ref the raw open is made with. Release runs the moment the
// privileged step has returned, success or failure.
type Grant interface {
	Release()
}

// NopGrant is the Grant of a host whose user authorization happened before
// the helper started (polkit, UAC).
type NopGrant struct{}

func (NopGrant) Release() {}

var (
	ErrUnauthorized = errors.New("helper: caller is not authorized")
	ErrIdle         = errors.New("helper: idle timeout")
)
