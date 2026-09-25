package helper

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/kyleaupton/flashit/internal/fatfmt"
	"github.com/kyleaupton/flashit/internal/helper/validate"
)

const (
	lockAttempts   = 5
	lockRetry      = time.Second
	rawBufferBytes = 4 << 20
)

var errVolumeBusy = errors.New("a volume on the disk is in use")

type windowsDisk struct {
	log          *slog.Logger
	lockAttempts int
	lockRetry    time.Duration
}

// NewDisk returns the Win32-backed Disk. Every path it is given has passed
// validate.PhysicalDrivePath.
func NewDisk(log *slog.Logger) (Disk, error) {
	return &windowsDisk{log: log, lockAttempts: lockAttempts, lockRetry: lockRetry}, nil
}

func (*windowsDisk) UnmountsBeforeEject() {}

func (d *windowsDisk) Stat(path string) (DeviceInfo, error) {
	if _, _, err := validate.PhysicalDrivePath(path); err != nil {
		return DeviceInfo{}, err
	}
	h, err := openDevice(path, windows.GENERIC_READ, 0)
	if err != nil {
		return DeviceInfo{}, err
	}
	defer windows.CloseHandle(h)
	bus, err := busType(h)
	if err != nil {
		return DeviceInfo{}, err
	}
	size, err := diskLength(h)
	if err != nil {
		return DeviceInfo{}, err
	}
	d.log.Info("disk", "device", path, "bus", bus, "size", size)
	return DeviceInfo{
		Path:      path,
		IsBlock:   true,
		WholeDisk: path,
		Size:      uint64(size),
		Removable: validate.RemovableBus(bus),
	}, nil
}

// SystemDisks is every disk holding the Windows volume, the system (EFI or
// boot) partition Windows started from, or a page file.
func (d *windowsDisk) SystemDisks() ([]string, error) {
	vols, err := systemVolumes()
	if err != nil {
		return nil, err
	}
	return validate.SystemDisks(vols, volumeDisks)
}

func systemVolumes() ([]string, error) {
	windir, err := windows.GetWindowsDirectory()
	if err != nil {
		return nil, fmt.Errorf("windows directory: %w", err)
	}
	root, err := volumeRoot(windir)
	if err != nil {
		return nil, err
	}
	vols := []string{volumeDevice(root)}

	// Setup records the partition the firmware booted from, as an NT
	// device path. Every install has it; without it we cannot tell.
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\Setup`, registry.QUERY_VALUE)
	if err != nil {
		return nil, fmt.Errorf("open HKLM\\SYSTEM\\Setup: %w", err)
	}
	sysPart, _, err := k.GetStringValue("SystemPartition")
	k.Close()
	if err != nil {
		return nil, fmt.Errorf("read SystemPartition: %w", err)
	}
	if !strings.HasPrefix(sysPart, `\Device\`) {
		return nil, fmt.Errorf("SystemPartition %q is not an NT device path", sysPart)
	}
	vols = append(vols, `\\?\GLOBALROOT`+sysPart)

	k, err = registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Memory Management`, registry.QUERY_VALUE)
	if err != nil {
		return nil, fmt.Errorf("open Memory Management: %w", err)
	}
	defer k.Close()
	var entries []string
	for _, name := range []string{"ExistingPageFiles", "PagingFiles"} {
		v, _, err := k.GetStringsValue(name)
		if errors.Is(err, registry.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		entries = append(entries, v...)
	}
	roots, err := validate.PageFileVolumes(entries, root[0])
	if err != nil {
		return nil, err
	}
	for _, r := range roots {
		vols = append(vols, volumeDevice(r))
	}
	return vols, nil
}

// volumeRoot is the root of the volume holding path, like C:\.
func volumeRoot(path string) (string, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	buf := make([]uint16, windows.MAX_PATH)
	if err := windows.GetVolumePathName(p, &buf[0], uint32(len(buf))); err != nil {
		return "", fmt.Errorf("volume of %s: %w", path, err)
	}
	root := windows.UTF16ToString(buf)
	if len(root) != 3 || root[1] != ':' || root[2] != '\\' {
		return "", fmt.Errorf("volume of %s is %q, not a drive letter", path, root)
	}
	return root, nil
}

// volumeDevice turns C:\ into \\.\C:.
func volumeDevice(root string) string { return `\\.\` + root[:2] }

func (d *windowsDisk) Partitions(string) ([]string, error) { return nil, nil }

// Unmount locks and dismounts every volume on the disk, then lets go: the
// volumes stay dismounted until something touches them. Busy volumes are
// retried here, so the error is not EBUSY and the session does not retry it
// again.
func (d *windowsDisk) Unmount(path string) error {
	_, n, err := validate.PhysicalDrivePath(path)
	if err != nil {
		return err
	}
	locks, err := d.lockVolumes(context.Background(), n)
	if err != nil {
		return err
	}
	locks.release()
	return nil
}

// volumeLocks holds locked, dismounted volumes; Windows cannot remount them
// until the handles close.
type volumeLocks struct {
	handles []windows.Handle
}

func (l *volumeLocks) release() {
	for _, h := range l.handles {
		windows.CloseHandle(h)
	}
	l.handles = nil
}

// lockVolumes locks and dismounts every volume with an extent on disk n.
// A volume someone holds open refuses the lock; that is retried a few times
// a second apart before it is errVolumeBusy.
func (d *windowsDisk) lockVolumes(ctx context.Context, n uint32) (*volumeLocks, error) {
	for attempt := 1; ; attempt++ {
		locks, err := d.tryLockVolumes(n)
		if err == nil || !errors.Is(err, errVolumeBusy) {
			return locks, err
		}
		if attempt >= d.lockAttempts {
			return nil, err
		}
		d.log.Info("volume busy, retrying", "disk", n, "attempt", attempt, "error", err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(d.lockRetry):
		}
	}
}

func (d *windowsDisk) tryLockVolumes(n uint32) (*volumeLocks, error) {
	vols, err := volumes()
	if err != nil {
		return nil, err
	}
	locks := &volumeLocks{}
	for _, v := range vols {
		disks, err := volumeDisks(v)
		if err != nil {
			// Volumes without media or of other kinds; one of ours that
			// stays mounted makes Windows refuse the raw write instead.
			d.log.Debug("skipping volume", "volume", v, "error", err)
			continue
		}
		if !onDisk(disks, n) {
			continue
		}
		h, err := openDevice(v, windows.GENERIC_READ|windows.GENERIC_WRITE, 0)
		if err != nil {
			locks.release()
			return nil, fmt.Errorf("%w: %v", errVolumeBusy, err)
		}
		if _, err := ioctl(h, fsctlLockVolume, nil, nil); err != nil {
			windows.CloseHandle(h)
			locks.release()
			return nil, fmt.Errorf("%w: lock %s: %v", errVolumeBusy, v, err)
		}
		if _, err := ioctl(h, fsctlDismountVolume, nil, nil); err != nil {
			windows.CloseHandle(h)
			locks.release()
			return nil, fmt.Errorf("dismount %s: %w", v, err)
		}
		d.log.Info("locked and dismounted", "volume", v, "disk", n)
		locks.handles = append(locks.handles, h)
	}
	return locks, nil
}

func onDisk(disks []uint32, n uint32) bool {
	for _, d := range disks {
		if d == n {
			return true
		}
	}
	return false
}

// openForWrite locks the disk's volumes, opens the disk for unbuffered
// writing and deletes its layout. The caller closes the disk and then
// releases the locks.
func (d *windowsDisk) openForWrite(ctx context.Context, device string) (windows.Handle, *volumeLocks, error) {
	_, n, err := validate.PhysicalDrivePath(device)
	if err != nil {
		return windows.InvalidHandle, nil, err
	}
	locks, err := d.lockVolumes(ctx, n)
	if err != nil {
		if errors.Is(err, errVolumeBusy) {
			err = fmt.Errorf("%w: %w", syscall.EBUSY, err)
		}
		return windows.InvalidHandle, nil, err
	}
	h, err := openDevice(device, windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_FLAG_NO_BUFFERING|windows.FILE_FLAG_WRITE_THROUGH)
	if err != nil {
		locks.release()
		return windows.InvalidHandle, nil, err
	}
	// The gate ran on an earlier handle; make sure this one is still the
	// same removable disk.
	if got, err := diskNumber(h); err != nil || got != n {
		windows.CloseHandle(h)
		locks.release()
		return windows.InvalidHandle, nil, fmt.Errorf("%s is not disk %d (%d, %v)", device, n, got, err)
	}
	if bus, err := busType(h); err != nil || !validate.RemovableBus(bus) {
		windows.CloseHandle(h)
		locks.release()
		return windows.InvalidHandle, nil, fmt.Errorf("%s is no longer a removable disk (bus %#x, %v)", device, bus, err)
	}
	// The held locks already keep the old volumes from remounting; this
	// also stops Windows mounting a half-written new layout. A blank disk
	// has no layout to delete, so a failure is only logged.
	if _, err := ioctl(h, ioctlDiskDeleteDriveLayout, nil, nil); err != nil {
		d.log.Info("IOCTL_DISK_DELETE_DRIVE_LAYOUT", "device", device, "error", err)
	}
	return h, locks, nil
}

// finish flushes the disk and has Windows re-read its new layout, which
// mounts the volumes on it again.
func finish(h windows.Handle) error {
	if err := windows.FlushFileBuffers(h); err != nil {
		return fmt.Errorf("flush: %w", err)
	}
	if _, err := ioctl(h, ioctlDiskUpdateProperties, nil, nil); err != nil {
		return fmt.Errorf("IOCTL_DISK_UPDATE_PROPERTIES: %w", err)
	}
	return nil
}

func (d *windowsDisk) OpenRaw(device string, _ Grant) (RawDevice, error) {
	h, locks, err := d.openForWrite(context.Background(), device)
	if err != nil {
		return nil, err
	}
	ss, err := sectorSize(h)
	if err != nil || ss <= 0 || ss > 4096 || ss&(ss-1) != 0 {
		windows.CloseHandle(h)
		locks.release()
		return nil, fmt.Errorf("sector size %d: %v", ss, err)
	}
	return &rawDisk{h: h, locks: locks, sector: int(ss), buf: alignedBuf(rawBufferBytes)}, nil
}

// rawDisk writes sequentially in whole, aligned sectors. The image's last
// partial sector is padded with zeros on Sync; the device is at least as
// large as the image rounded up, since its size is a whole number of
// sectors.
type rawDisk struct {
	h      windows.Handle
	locks  *volumeLocks
	sector int
	buf    []byte
	fill   int
}

func (r *rawDisk) Write(p []byte) (int, error) {
	n := 0
	for len(p) > 0 {
		c := copy(r.buf[r.fill:], p)
		r.fill += c
		p = p[c:]
		if r.fill == len(r.buf) {
			if err := r.flush(); err != nil {
				return n, err
			}
		}
		n += c
	}
	return n, nil
}

func (r *rawDisk) flush() error {
	if r.fill == 0 {
		return nil
	}
	if rem := r.fill % r.sector; rem != 0 {
		clear(r.buf[r.fill : r.fill+r.sector-rem])
		r.fill += r.sector - rem
	}
	var done uint32
	err := windows.WriteFile(r.h, r.buf[:r.fill], &done, nil)
	if err == nil && int(done) != r.fill {
		err = fmt.Errorf("short write: %d of %d bytes", done, r.fill)
	}
	r.fill = 0
	return err
}

func (r *rawDisk) Sync() error {
	if err := r.flush(); err != nil {
		return err
	}
	return finish(r.h)
}

// Close after a failed write still has Windows re-read the disk, so it does
// not keep treating it as blank until the stick is replugged.
func (r *rawDisk) Close() error {
	_, _ = ioctl(r.h, ioctlDiskUpdateProperties, nil, nil)
	err := windows.CloseHandle(r.h)
	r.locks.release()
	return err
}

func (d *windowsDisk) Format(ctx context.Context, device, fs, label string) (string, error) {
	if fs != "fat32" {
		return "", fmt.Errorf("unsupported filesystem %q", fs)
	}
	h, locks, err := d.openForWrite(ctx, device)
	if err != nil {
		return "", err
	}
	defer locks.release()
	defer windows.CloseHandle(h)
	size, err := diskLength(h)
	if err != nil {
		return "", err
	}
	ss, err := sectorSize(h)
	if err != nil {
		return "", err
	}
	d.log.Info("formatting", "device", device, "size", size, "sector", ss, "label", label)
	if err := fatfmt.Format(&diskIO{h: h}, size, ss, label); err != nil {
		return "", err
	}
	if err := finish(h); err != nil {
		return "", err
	}
	// Windows mounts the new volume and gives it a letter by itself; the
	// app waits for that.
	return "", nil
}

// diskIO is fatfmt's view of the disk: positional, whole-sector I/O through
// aligned bounce buffers.
type diskIO struct {
	h windows.Handle
}

func (d *diskIO) ReadAt(p []byte, off int64) (int, error) {
	buf := alignedBuf(len(p))
	var n uint32
	if err := windows.ReadFile(d.h, buf, &n, overlappedAt(off)); err != nil {
		return 0, fmt.Errorf("read %d bytes at %d: %w", len(p), off, err)
	}
	if int(n) != len(p) {
		return int(n), fmt.Errorf("short read at %d: %d of %d bytes", off, n, len(p))
	}
	return copy(p, buf), nil
}

func (d *diskIO) WriteAt(p []byte, off int64) (int, error) {
	buf := alignedBuf(len(p))
	copy(buf, p)
	var n uint32
	if err := windows.WriteFile(d.h, buf, &n, overlappedAt(off)); err != nil {
		return 0, fmt.Errorf("write %d bytes at %d: %w", len(p), off, err)
	}
	if int(n) != len(p) {
		return int(n), fmt.Errorf("short write at %d: %d of %d bytes", off, n, len(p))
	}
	return len(p), nil
}

// overlappedAt carries the offset for a synchronous handle; the call still
// blocks until done.
func overlappedAt(off int64) *windows.Overlapped {
	return &windows.Overlapped{Offset: uint32(off), OffsetHigh: uint32(off >> 32)}
}

// Eject locks and dismounts the volumes again, since something may have
// remounted them since the unmount, then ejects the media.
func (d *windowsDisk) Eject(device string) error {
	_, n, err := validate.PhysicalDrivePath(device)
	if err != nil {
		return err
	}
	locks, err := d.lockVolumes(context.Background(), n)
	if err != nil {
		return err
	}
	defer locks.release()
	h, err := openDevice(device, windows.GENERIC_READ, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	if _, err := ioctl(h, ioctlStorageMediaRemoval, []byte{0, 0, 0, 0}, nil); err != nil {
		d.log.Info("allow media removal", "device", device, "error", err)
	}
	if _, err := ioctl(h, ioctlStorageEjectMedia, nil, nil); err != nil {
		return fmt.Errorf("IOCTL_STORAGE_EJECT_MEDIA: %w", err)
	}
	return nil
}
