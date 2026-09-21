// Package validate holds the helper's security checks as pure functions so
// every rejection can be table-tested without a block device.
package validate

import (
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kyleaupton/flashit/internal/proto"
)

// DeviceInfo is what the helper learned about a device from the OS after
// resolving symlinks. Path is canonical; WholeDisk equals Path when the device
// is itself a whole disk.
type DeviceInfo struct {
	Path      string
	IsBlock   bool
	WholeDisk string
	Size      uint64
	Removable bool
}

const devRoot = "/dev/"

var labelRe = regexp.MustCompile(`^[A-Za-z0-9_ -]{1,11}$`)

// DevicePath rejects anything that is not a clean absolute path under /dev.
// It runs on the path the client sent and again on the canonical path the OS
// resolved it to.
func DevicePath(path string) (string, error) {
	if path == "" {
		return "", proto.NewError(proto.CodeInvalidDevice, "device path is empty")
	}
	if strings.ContainsRune(path, 0) {
		return "", proto.NewError(proto.CodeInvalidDevice, "device path contains NUL")
	}
	if !filepath.IsAbs(path) {
		return "", proto.Errorf(proto.CodeInvalidDevice, "device path %q is not absolute", path)
	}
	for _, part := range strings.Split(path, "/") {
		if part == ".." || part == "." {
			return "", proto.Errorf(proto.CodeInvalidDevice, "device path %q contains %q", path, part)
		}
	}
	clean := filepath.Clean(path)
	if clean != path {
		return "", proto.Errorf(proto.CodeInvalidDevice, "device path %q is not canonical", path)
	}
	rest := strings.TrimPrefix(clean, devRoot)
	if rest == clean || rest == "" {
		return "", proto.Errorf(proto.CodeInvalidDevice, "device path %q is not under /dev", path)
	}
	return clean, nil
}

// Target checks a resolved device against everything the helper refuses to
// touch: it must be a whole, removable block device that backs no system
// mount. An empty systemDisks list means the OS could not tell us what the
// system disk is, and that refuses everything.
func Target(info DeviceInfo, systemDisks []string) error {
	if _, err := DevicePath(info.Path); err != nil {
		return err
	}
	if !info.IsBlock {
		return proto.Errorf(proto.CodeInvalidDevice, "%s is not a block device", info.Path)
	}
	if info.WholeDisk != info.Path {
		return proto.Errorf(proto.CodeInvalidDevice, "%s is a partition of %s, not a whole disk", info.Path, info.WholeDisk)
	}
	if !info.Removable {
		return proto.Errorf(proto.CodeNotRemovable, "%s is not a removable device", info.Path)
	}
	if len(systemDisks) == 0 {
		return proto.Errorf(proto.CodeSystemDisk, "cannot determine the system disk, refusing %s", info.Path)
	}
	for _, sys := range systemDisks {
		if sys == "" {
			return proto.Errorf(proto.CodeSystemDisk, "cannot determine the system disk, refusing %s", info.Path)
		}
		if info.WholeDisk == sys {
			return proto.Errorf(proto.CodeSystemDisk, "refusing to write to %s (system disk)", info.Path)
		}
	}
	return nil
}

// Source checks the image the app handed over as an open file: a regular
// file of the size the request claims. The helper runs as root, so no
// message reveals the real size.
func Source(fi fs.FileInfo, size int64) error {
	if !fi.Mode().IsRegular() {
		return proto.NewError(proto.CodeInvalidSource, "the image is not a regular file")
	}
	if size <= 0 {
		return proto.Errorf(proto.CodeInvalidSource, "size %d is not positive", size)
	}
	if fi.Size() != size {
		return proto.NewError(proto.CodeSizeMismatch, "the image does not have the size the request claims")
	}
	return nil
}

// Capacity checks that size bytes fit on the device.
func Capacity(size int64, dev DeviceInfo) error {
	if dev.Size == 0 {
		return proto.Errorf(proto.CodeInsufficientCapacity, "size of %s is unknown", dev.Path)
	}
	if size < 0 || uint64(size) > dev.Size {
		return proto.Errorf(proto.CodeInsufficientCapacity, "%d bytes do not fit on %s (%d bytes)", size, dev.Path, dev.Size)
	}
	return nil
}

func Label(label string) error {
	if !labelRe.MatchString(label) {
		return proto.Errorf(proto.CodeInvalidLabel, "label %q must match %s", label, labelRe.String())
	}
	return nil
}

// Filesystem returns the normalized filesystem name; only fat32 is supported.
func Filesystem(fs string) (string, error) {
	if strings.EqualFold(fs, "fat32") {
		return "fat32", nil
	}
	return "", proto.Errorf(proto.CodeInvalidRequest, "unsupported filesystem %q", fs)
}

// Mountpoint accepts only a directory format_disk could have created:
// MountRoot followed by one valid label.
func Mountpoint(path string) (string, error) {
	if path == "" || strings.ContainsRune(path, 0) || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", proto.Errorf(proto.CodeInvalidRequest, "mountpoint %q is not a clean absolute path", path)
	}
	rest := strings.TrimPrefix(path, MountRoot+"/")
	if rest == path {
		return "", proto.Errorf(proto.CodeInvalidRequest, "mountpoint %q is not under %s", path, MountRoot)
	}
	if err := Label(rest); err != nil {
		return "", proto.Errorf(proto.CodeInvalidRequest, "mountpoint %q is not a label directory under %s", path, MountRoot)
	}
	return path, nil
}
