//go:build linux

package helper

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/kyleaupton/flashit/internal/helper/validate"
)

const (
	sysBlock      = "/sys/block"
	sysClassBlock = "/sys/class/block"
	sysDevBlock   = "/sys/dev/block"
	mountInfo     = "/proc/self/mountinfo"
	blkRRPart     = 0x125f
	// Flush to the device this often so the page cache cannot swallow the
	// whole image and make progress lie.
	rawSyncEvery = 64 << 20
)

type linuxDisk struct {
	uid, gid int
	log      *slog.Logger
}

// NewDisk returns the sysfs-backed Disk. uid is the unprivileged user that
// will use volumes format_disk mounts.
func NewDisk(uid int, log *slog.Logger) (Disk, error) {
	gid := uid
	if u, err := user.LookupId(strconv.Itoa(uid)); err == nil {
		if g, err := strconv.Atoi(u.Gid); err == nil {
			gid = g
		}
	}
	return &linuxDisk{uid: uid, gid: gid, log: log}, nil
}

func (d *linuxDisk) Stat(path string) (DeviceInfo, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return DeviceInfo{}, err
	}
	fi, err := os.Stat(resolved)
	if err != nil {
		return DeviceInfo{}, err
	}
	info := DeviceInfo{Path: resolved, WholeDisk: resolved}
	info.IsBlock = fi.Mode()&os.ModeDevice != 0 && fi.Mode()&os.ModeCharDevice == 0
	if !info.IsBlock {
		return info, nil
	}

	name := filepath.Base(resolved)
	sysPath, err := filepath.EvalSymlinks(filepath.Join(sysClassBlock, name))
	if err != nil {
		return DeviceInfo{}, fmt.Errorf("sysfs entry for %s: %w", resolved, err)
	}
	whole := name
	if _, err := os.Stat(filepath.Join(sysPath, "partition")); err == nil {
		whole = filepath.Base(filepath.Dir(sysPath))
	}
	info.WholeDisk = "/dev/" + whole

	sectors, err := readUint(filepath.Join(sysPath, "size"))
	if err != nil {
		return DeviceInfo{}, fmt.Errorf("size of %s: %w", resolved, err)
	}
	info.Size = sectors * 512
	info.Removable = isRemovable(whole)
	return info, nil
}

// isRemovable mirrors what lsblk reports as removable or hotplug: the sysfs
// removable flag, or any USB ancestor in the device chain.
func isRemovable(whole string) bool {
	if v, err := readUint(filepath.Join(sysBlock, whole, "removable")); err == nil && v == 1 {
		return true
	}
	dev, err := filepath.EvalSymlinks(filepath.Join(sysBlock, whole, "device"))
	if err != nil {
		return false
	}
	for dir := dev; dir != "/" && dir != "/sys"; dir = filepath.Dir(dir) {
		sub, err := os.Readlink(filepath.Join(dir, "subsystem"))
		if err == nil && filepath.Base(sub) == "usb" {
			return true
		}
	}
	return false
}

func (d *linuxDisk) SystemDisks() ([]string, error) {
	mounts, err := readMountInfo()
	if err != nil {
		return nil, err
	}
	return systemDisksFrom(mounts, sysfs{devBlock: sysDevBlock, classBlock: sysClassBlock, resolve: filepath.EvalSymlinks})
}

func readMountInfo() ([]mount, error) {
	f, err := os.Open(mountInfo)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return parseMountInfo(f)
}

func (d *linuxDisk) Partitions(device string) ([]string, error) {
	return partitionsOf(sysBlock, filepath.Base(device))
}

// Unmount takes a device path or a mountpoint. Mounts are matched by
// major:minor so /dev/mapper style aliases still count. There is no lazy
// fallback: a mount that will not let go stops the op.
func (d *linuxDisk) Unmount(path string) error {
	if !strings.HasPrefix(path, "/dev/") {
		err := unix.Unmount(path, 0)
		if errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOENT) {
			return nil
		}
		return err
	}

	mounts, err := mountsOf(path)
	if err != nil {
		return err
	}
	for i := len(mounts) - 1; i >= 0; i-- {
		d.log.Info("unmounting", "device", path, "mountpoint", mounts[i].mountpoint)
		if err := unix.Unmount(mounts[i].mountpoint, 0); err != nil {
			return fmt.Errorf("umount %s: %w", mounts[i].mountpoint, err)
		}
	}
	return nil
}

func (d *linuxDisk) Mounts(device string) ([]string, error) {
	mounts, err := mountsOf(device)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(mounts))
	for i, m := range mounts {
		out[i] = m.mountpoint
	}
	return out, nil
}

func (d *linuxDisk) RemoveMountDir(dir string) error {
	return removeMountDir(validate.MountRoot, dir)
}

// mountsOf returns the mount table entries whose major:minor is device's.
func mountsOf(device string) ([]mount, error) {
	var st unix.Stat_t
	if err := unix.Stat(device, &st); err != nil {
		return nil, err
	}
	majMin := fmt.Sprintf("%d:%d", unix.Major(uint64(st.Rdev)), unix.Minor(uint64(st.Rdev)))
	all, err := readMountInfo()
	if err != nil {
		return nil, err
	}
	var mounts []mount
	for _, m := range all {
		if m.majMin == majMin {
			mounts = append(mounts, m)
		}
	}
	return mounts, nil
}

func (d *linuxDisk) OpenRaw(device string, _ Grant) (RawDevice, error) {
	f, err := os.OpenFile(device, os.O_WRONLY|unix.O_EXCL, 0)
	if err != nil {
		return nil, err
	}
	return &rawFile{File: f}, nil
}

type rawFile struct {
	*os.File
	unsynced int64
}

func (r *rawFile) Write(p []byte) (int, error) {
	n, err := r.File.Write(p)
	r.unsynced += int64(n)
	if err == nil && r.unsynced >= rawSyncEvery {
		r.unsynced = 0
		err = unix.Fdatasync(int(r.Fd()))
	}
	return n, err
}

func (d *linuxDisk) Format(ctx context.Context, device, fs, label string) (string, error) {
	if fs != "fat32" {
		return "", fmt.Errorf("unsupported filesystem %q", fs)
	}
	steps := [][]string{
		{"parted", "--script", device, "mklabel", "msdos"},
		{"parted", "--script", "--align", "optimal", device, "mkpart", "primary", "fat32", "1MiB", "100%"},
		{"parted", "--script", device, "set", "1", "boot", "on"},
	}
	for _, argv := range steps {
		if err := run(ctx, d.log, argv...); err != nil {
			return "", err
		}
	}
	_ = run(ctx, d.log, "partprobe", device)
	_ = run(ctx, d.log, "udevadm", "settle", "--timeout=5")

	part := partitionPath(device, 1)
	if err := waitForPath(ctx, part, 5*time.Second); err != nil {
		return "", err
	}
	if err := run(ctx, d.log, "mkfs.vfat", "-F", "32", "-n", label, part); err != nil {
		return "", err
	}

	mountpoint := filepath.Join(validate.MountRoot, label)
	if err := os.MkdirAll(mountpoint, 0o755); err != nil {
		return "", err
	}
	// Only the helper populates MountRoot, so anything mounted here is a
	// leftover of ours and can go; if it will not, the device is busy.
	if isMountpoint(mountpoint) {
		d.log.Info("unmounting stale mount", "mountpoint", mountpoint)
		if err := unix.Unmount(mountpoint, 0); err != nil {
			return "", fmt.Errorf("unmount stale %s: %w", mountpoint, unix.EBUSY)
		}
	}
	opts := fmt.Sprintf("uid=%d,gid=%d", d.uid, d.gid)
	if err := unix.Mount(part, mountpoint, "vfat", unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC, opts); err != nil {
		return "", fmt.Errorf("mount %s at %s: %w", part, mountpoint, err)
	}
	return mountpoint, nil
}

// isMountpoint reports whether path sits on a different device than its
// parent directory.
func isMountpoint(path string) bool {
	var self, parent unix.Stat_t
	if err := unix.Stat(path, &self); err != nil {
		return false
	}
	if err := unix.Stat(filepath.Dir(path), &parent); err != nil {
		return false
	}
	return self.Dev != parent.Dev
}

func waitForPath(ctx context.Context, path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s did not appear", path)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// Eject flushes and, when eject(1) is installed, asks the kernel to re-read
// the partition table and spins the device down. Without eject(1) the
// re-read is skipped: it re-announces the partitions, which a desktop
// automounter may mount again. The eject op has unmounted everything by
// then, so none of this is needed for a safe pull.
func (d *linuxDisk) Eject(device string) error {
	unix.Sync()
	if _, err := exec.LookPath("eject"); err != nil {
		d.log.Info("eject not installed, leaving the unmounted device attached", "device", device)
		return nil
	}
	if f, err := os.OpenFile(device, os.O_RDONLY|unix.O_NONBLOCK, 0); err == nil {
		if _, err := unix.IoctlRetInt(int(f.Fd()), blkRRPart); err != nil {
			d.log.Warn("re-read partition table", "device", device, "error", err)
		}
		f.Close()
	}
	return run(context.Background(), d.log, "eject", device)
}

func run(ctx context.Context, log *slog.Logger, argv ...string) error {
	log.Info("exec", "argv", argv)
	out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", argv[0], err, strings.TrimSpace(string(out)))
	}
	return nil
}
