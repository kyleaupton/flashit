//go:build darwin

package helper

/*
#include <stdlib.h>
#include "da_darwin.h"
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

const diskutilPath = "/usr/sbin/diskutil"

var wholeDiskRe = regexp.MustCompile(`^/dev/disk[0-9]+$`)

type darwinDisk struct {
	du  diskutil
	log *slog.Logger
}

// NewDisk returns the diskutil-backed Disk.
func NewDisk(log *slog.Logger) (Disk, error) {
	d := &darwinDisk{log: log}
	d.du = diskutil{run: func(ctx context.Context, args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, diskutilPath, args...).Output()
	}}
	return d, nil
}

func (d *darwinDisk) Stat(path string) (DeviceInfo, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return DeviceInfo{}, err
	}
	fi, err := os.Stat(resolved)
	if err != nil {
		return DeviceInfo{}, err
	}
	// /dev/rdiskN is the character node; only the block node is a target.
	if fi.Mode()&os.ModeDevice == 0 || fi.Mode()&os.ModeCharDevice != 0 {
		return DeviceInfo{Path: resolved, WholeDisk: resolved}, nil
	}
	inf, err := d.du.info(context.Background(), resolved)
	if err != nil {
		return DeviceInfo{}, err
	}
	return deviceInfoFrom(resolved, inf)
}

func (d *darwinDisk) SystemDisks() ([]string, error) {
	var st unix.Statfs_t
	if err := unix.Statfs("/", &st); err != nil {
		return nil, err
	}
	return d.du.systemDisks(context.Background(), unix.ByteSliceToString(st.Mntfromname[:]))
}

// Partitions reports none: `diskutil unmountDisk` takes the whole disk and
// everything on it in one call, so there is no per-partition pass here.
func (d *darwinDisk) Partitions(string) ([]string, error) {
	return nil, nil
}

// Unmount takes a device path or a mountpoint. A whole disk is unmounted
// with everything on it, including any synthesized APFS container backed
// by one of its partitions, which would otherwise keep the disk busy.
func (d *darwinDisk) Unmount(path string) error {
	ctx := context.Background()
	if !strings.HasPrefix(path, "/dev/") {
		return d.diskutilUnmount(ctx, "unmount", path)
	}
	if !wholeDiskRe.MatchString(path) {
		return d.diskutilUnmount(ctx, "unmount", path)
	}
	containers, err := d.du.containersOn(ctx, path)
	if err != nil {
		return err
	}
	for _, c := range containers {
		d.log.Info("unmounting APFS container", "container", c, "disk", path)
		if err := d.diskutilUnmount(ctx, "unmountDisk", c); err != nil {
			return err
		}
	}
	return d.diskutilUnmount(ctx, "unmountDisk", path)
}

// diskutilUnmount runs `diskutil <verb> force <target>`. diskutil reports a
// target that was not mounted as an error in some versions; that is not
// one for us.
func (d *darwinDisk) diskutilUnmount(ctx context.Context, verb, target string) error {
	out, err := d.run(ctx, diskutilPath, verb, "force", target)
	if err == nil {
		return nil
	}
	lower := strings.ToLower(out)
	if strings.Contains(lower, "not mounted") || strings.Contains(lower, "already unmounted") {
		return nil
	}
	return fmt.Errorf("%w: %v", syscall.EBUSY, err)
}

// OpenRaw claims the disk through Disk Arbitration, so nothing remounts it
// while it is written, then opens the character node, which bypasses the
// buffer cache. The claim is released when the device is closed.
func (d *darwinDisk) OpenRaw(device string) (RawDevice, error) {
	if !wholeDiskRe.MatchString(device) {
		return nil, fmt.Errorf("%s is not a whole disk", device)
	}
	bsd := filepath.Base(device)
	cbsd := C.CString(bsd)
	defer C.free(unsafe.Pointer(cbsd))
	var (
		claim  *C.flashit_da_handle
		errbuf [256]C.char
	)
	if rc := C.flashit_da_claim(cbsd, &claim, &errbuf[0], C.size_t(len(errbuf))); rc != 0 {
		return nil, fmt.Errorf("%w: %s", syscall.EBUSY, C.GoString(&errbuf[0]))
	}
	f, err := os.OpenFile("/dev/r"+bsd, os.O_WRONLY|unix.O_EXCL, 0)
	if err != nil {
		C.flashit_da_unclaim(claim)
		return nil, err
	}
	d.log.Info("claimed and opened", "device", device, "raw", f.Name())
	return &rawDisk{File: f, claim: claim}, nil
}

type rawDisk struct {
	*os.File
	claim *C.flashit_da_handle
}

// Sync asks the drive to flush its own cache. Raw writes bypass the buffer
// cache, so a device that does not support the flush has nothing to lose.
func (r *rawDisk) Sync() error {
	fd := int(r.Fd())
	_, err := unix.FcntlInt(uintptr(fd), unix.F_FULLFSYNC, 0)
	if err == nil {
		return nil
	}
	if err := unix.Fsync(fd); err == nil {
		return nil
	} else if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOTTY) {
		return nil
	} else {
		return err
	}
}

func (r *rawDisk) Close() error {
	err := r.File.Close()
	C.flashit_da_unclaim(r.claim)
	r.claim = nil
	return err
}

// Format erases the disk with a single FAT32 partition under an MBR and
// returns where macOS mounted it. diskutil insists on upper-case FAT labels.
func (d *darwinDisk) Format(ctx context.Context, device, fs, label string) (string, error) {
	if fs != "fat32" {
		return "", fmt.Errorf("unsupported filesystem %q", fs)
	}
	if _, err := d.run(ctx, diskutilPath, "eraseDisk", "FAT32", strings.ToUpper(label), "MBR", device); err != nil {
		return "", err
	}
	part := device + "s1"
	deadline := time.Now().Add(15 * time.Second)
	for {
		mp, err := d.du.mountpoint(ctx, part)
		if err == nil && mp != "" {
			return mp, nil
		}
		if time.Now().After(deadline) {
			if err != nil {
				return "", fmt.Errorf("%s was not mounted after format: %w", part, err)
			}
			return "", fmt.Errorf("%s was not mounted after format", part)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func (d *darwinDisk) Eject(device string) error {
	_, err := d.run(context.Background(), diskutilPath, "eject", device)
	return err
}

func (d *darwinDisk) run(ctx context.Context, argv ...string) (string, error) {
	d.log.Info("exec", "argv", argv)
	out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		return text, fmt.Errorf("%s: %w: %s", filepath.Base(argv[0]), err, text)
	}
	return text, nil
}
