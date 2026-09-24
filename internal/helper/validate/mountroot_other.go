//go:build !darwin

package validate

// MountRoot is the only place format_disk mounts and unmount will unmount.
const MountRoot = "/run/media/flashit"
