package iso

import "context"

// MountResult holds the result of mounting an ISO image.
type MountResult struct {
	MountPath string // Where the ISO contents are accessible
	DevEntry  string // Device entry (for unmounting on macOS)
}

// Mounter provides cross-platform ISO mount operations.
type Mounter interface {
	// Mount mounts the ISO image and returns the mount result.
	Mount(ctx context.Context, isoPath string) (*MountResult, error)

	// Unmount unmounts a previously mounted ISO.
	Unmount(ctx context.Context, result *MountResult) error

	// DetachExisting detaches any existing mount of the given ISO path.
	// Returns the device entry if found, empty string if not mounted.
	DetachExisting(ctx context.Context, isoPath string) string

	// IsSupported returns true if ISO mounting is implemented for this platform.
	IsSupported() bool
}

// Default, platform-specific mounter (set by per-OS file).
var defaultMounter Mounter = platformMounter()

// Mount mounts an ISO image and returns the mount result.
func Mount(ctx context.Context, isoPath string) (*MountResult, error) {
	return defaultMounter.Mount(ctx, isoPath)
}

// Unmount unmounts a previously mounted ISO.
func Unmount(ctx context.Context, result *MountResult) error {
	return defaultMounter.Unmount(ctx, result)
}

// DetachExisting detaches any existing mount of the given ISO path.
func DetachExisting(ctx context.Context, isoPath string) string {
	return defaultMounter.DetachExisting(ctx, isoPath)
}

// IsMountSupported returns true if ISO mounting is implemented for this platform.
func IsMountSupported() bool {
	return defaultMounter.IsSupported()
}
