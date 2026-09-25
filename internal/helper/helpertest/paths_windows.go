package helpertest

const (
	// Removable is a 1 MiB removable whole disk with two partitions.
	Removable = `\\.\PhysicalDrive2`
	// RemovableLink stands in for the Unix symlink; Stat resolves it to
	// Removable. Only the Unix server tests use it.
	RemovableLink = `\\.\PhysicalDrive7`
	// Internal is a non-removable whole disk.
	Internal = `\\.\PhysicalDrive1`
	// System is the whole disk backing the Windows volume.
	System = `\\.\PhysicalDrive0`
)
