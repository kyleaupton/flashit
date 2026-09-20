//go:build linux

package linux

import (
	"context"
	"errors"
)

// ErrCancelled is returned when an operation is cancelled by the user.
var ErrCancelled = errors.New("operation cancelled")

// ErrHelperNotRunning is returned when the helper process is not running.
var ErrHelperNotRunning = errors.New("privileged helper is not running")

// ProgressFunc is the callback type for write progress updates.
// bytesWritten: total bytes written so far
// totalBytes: total size of the ISO
type ProgressFunc func(bytesWritten, totalBytes uint64)

// Client defines the Unix socket client interface to the privileged helper.
//
// Design: The helper runs as a separate elevated process spawned via pkexec,
// communicating over a Unix domain socket using JSON messages.
type Client interface {
	// EnsureReady spawns the helper process with pkexec elevation if not already running,
	// and verifies communication is working.
	EnsureReady(ctx context.Context) error

	// WriteISO writes an ISO image to a raw disk device.
	// The progress callback receives (bytesWritten, totalBytes) updates during the write.
	// Pass nil if progress updates are not needed.
	WriteISO(ctx context.Context, isoPath string, device string, progress ProgressFunc) error

	// FormatDisk formats a disk with the specified filesystem and volume name.
	// Supported filesystems: FAT32, NTFS, exFAT
	FormatDisk(ctx context.Context, device string, filesystem string, volumeName string) error

	// CancelCurrentOperation cancels the currently running operation (if any).
	// This is safe to call even if no operation is running.
	CancelCurrentOperation()

	// Shutdown gracefully terminates the helper process.
	Shutdown(ctx context.Context) error
}
