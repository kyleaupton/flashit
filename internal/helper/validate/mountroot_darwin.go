//go:build darwin

package validate

// MountRoot is where diskutil mounts the volume format_disk made, and the
// only place unmount will unmount by path.
const MountRoot = "/Volumes"
