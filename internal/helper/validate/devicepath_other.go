//go:build !windows

package validate

func hostDevicePath(path string) (string, error) { return UnixDevicePath(path) }
