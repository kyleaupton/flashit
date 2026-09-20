// Package helper is the root-side server: NDJSON over one connection, one op
// at a time, every op validated before the OS is touched. The OS sits behind
// Disk and Auth so the whole package tests on any platform with fakes.
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
	// OpenRaw opens the device O_WRONLY|O_EXCL. It returns syscall.EBUSY
	// when the kernel still has the device claimed.
	OpenRaw(device string) (RawDevice, error)
	// Format writes an MBR with one active fat32 partition, formats it with
	// label, mounts it under validate.MountRoot and returns the mountpoint.
	Format(ctx context.Context, device, fs, label string) (string, error)
	Eject(device string) error
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

// Authorizer decides whether the user approved a destructive op. token is
// the request's authorization field, verbatim; the authorizer is free to
// prompt. It runs after every other check has passed so a refused request
// never costs the user a prompt.
type Authorizer interface {
	Authorize(op proto.Op, token string) error
}

var (
	ErrUnauthorized = errors.New("helper: caller is not authorized")
	ErrIdle         = errors.New("helper: idle timeout")
	// ErrConfig marks a failure that a restart will not fix: the build or
	// the host is wrong. The daemon exits cleanly on it so launchd does not
	// spin it.
	ErrConfig = errors.New("helper: configuration error")
)
