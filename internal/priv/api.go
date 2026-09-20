package priv

import (
	"context"
	"errors"
)

// ErrHelperNotRunning is returned by DiskOps when EnsureReady has not
// succeeded for this session.
var ErrHelperNotRunning = errors.New("privileged helper is not running")

// ProgressFunc receives (bytesWritten, totalBytes) during a write.
type ProgressFunc func(bytesWritten, totalBytes uint64)

// DiskOps is the privileged operation set the installers can reach.
type DiskOps interface {
	// WriteISO streams isoPath onto the whole device. Unmounting the device's
	// partitions and syncing at the end are the helper's job.
	WriteISO(ctx context.Context, isoPath string, device string, progress ProgressFunc) error

	// FormatDisk writes a single-partition layout with the given filesystem
	// and label. Supported values depend on the host helper; every host
	// accepts FAT32.
	FormatDisk(ctx context.Context, device string, filesystem string, volumeName string) error

	// Eject detaches the device once writing is done.
	Eject(ctx context.Context, device string) error
}

// PrivilegedService provides access to privileged operations.
// EnsureReady should perform any elevation/installation needed to execute
// privileged operations for this session.
type PrivilegedService interface {
	EnsureReady(ctx context.Context) error
	Disk() DiskOps
	Shutdown(ctx context.Context) error
}

var defaultService PrivilegedService = platformService()

// NewService returns a process-wide singleton service implementation so that
// elevation/authorization can be requested once and reused by all steps.
func NewService() PrivilegedService { return defaultService }
