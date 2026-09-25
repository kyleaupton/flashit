//go:build !windows

package helpertest

const (
	// Removable is a 1 MiB removable whole disk with two partitions.
	Removable = "/dev/sdb"
	// RemovableLink is a symlink that Stat resolves to Removable.
	RemovableLink = "/dev/disk/by-id/usb-Fake"
	// Internal is a non-removable whole disk.
	Internal = "/dev/sda"
	// System is the whole disk backing "/".
	System = "/dev/nvme0n1"
)
