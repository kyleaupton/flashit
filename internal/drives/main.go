package drives

import (
	"context"
	"time"
)

// Drive is a normalized, cross-platform description of a physical drive.
// For macOS, BSDName is like "disk3"; on Linux, it might be "sdb".
type Drive struct {
	Device      string // e.g. "/dev/disk3" (darwin), "/dev/sdb" (linux)
	BSDName     string // short name: "disk3" (darwin), "sdb" (linux), may be empty on Windows
	SizeBytes   uint64
	Model       string
	Vendor      string
	Serial      string
	Protocol    string // "USB", "SATA", "NVMe", "Thunderbolt", etc.
	IsRemovable bool
	IsEjectable bool
	Mountpoints []string // mountpoints for any child volumes (if known)
}

// Provider lists removable USB drives for the current platform.
type Provider interface {
	// Discovery
	ListRemovable(ctx context.Context) ([]Drive, error)

	// Operations
	Unmount(ctx context.Context, device string) error
	Eject(ctx context.Context, device string) error
	FindMountPoint(ctx context.Context, device string) (string, error)
	WaitForMount(ctx context.Context, device string, timeout time.Duration) (string, error)

	// Capability
	IsSupported() bool
}

// Default, platform-specific provider (set by per-OS file).
var defaultProvider Provider = platformProvider()

// ListRemovable returns removable USB whole media for the current OS using the default provider.
func ListRemovable(ctx context.Context) ([]Drive, error) {
	return defaultProvider.ListRemovable(ctx)
}

// Unmount unmounts all volumes on the given device.
func Unmount(ctx context.Context, device string) error {
	return defaultProvider.Unmount(ctx, device)
}

// Eject ejects the given device.
func Eject(ctx context.Context, device string) error {
	return defaultProvider.Eject(ctx, device)
}

// FindMountPoint finds the mount point for the given device or its first partition.
func FindMountPoint(ctx context.Context, device string) (string, error) {
	return defaultProvider.FindMountPoint(ctx, device)
}

// WaitForMount waits for the given device to be mounted, polling until timeout.
func WaitForMount(ctx context.Context, device string, timeout time.Duration) (string, error) {
	return defaultProvider.WaitForMount(ctx, device, timeout)
}

// IsSupported returns true if the drives module is implemented for this platform.
func IsSupported() bool {
	return defaultProvider.IsSupported()
}

// SetProvider allows tests or callers to swap the provider implementation (e.g., a mock).
func SetProvider(p Provider) {
	if p == nil {
		return
	}
	defaultProvider = p
}
