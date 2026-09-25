package helper

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	ioctlStorageQueryProperty       = 0x2d1400
	ioctlStorageGetDeviceNumber     = 0x2d1080
	ioctlStorageMediaRemoval        = 0x2d4804
	ioctlStorageEjectMedia          = 0x2d4808
	ioctlDiskGetLengthInfo          = 0x7405c
	ioctlDiskGetDriveGeometryEx     = 0x700a0
	ioctlDiskDeleteDriveLayout      = 0x7c100
	ioctlDiskUpdateProperties       = 0x70140
	ioctlVolumeGetVolumeDiskExtents = 0x560000
	fsctlLockVolume                 = 0x90018
	fsctlDismountVolume             = 0x90020

	fileDeviceDisk = 0x07
)

func ioctl(h windows.Handle, code uint32, in, out []byte) (int, error) {
	var inp, outp *byte
	if len(in) > 0 {
		inp = &in[0]
	}
	if len(out) > 0 {
		outp = &out[0]
	}
	var n uint32
	err := windows.DeviceIoControl(h, code, inp, uint32(len(in)), outp, uint32(len(out)), &n, nil)
	return int(n), err
}

// openDevice opens a disk or volume by its \\.\ path. access 0 is enough
// for the FILE_ANY_ACCESS queries and does not mount anything.
func openDevice(path string, access, flags uint32) (windows.Handle, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return windows.InvalidHandle, err
	}
	h, err := windows.CreateFile(p, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil,
		windows.OPEN_EXISTING, flags, 0)
	if err != nil {
		return windows.InvalidHandle, fmt.Errorf("open %s: %w", path, err)
	}
	return h, nil
}

// busType is STORAGE_DEVICE_DESCRIPTOR.BusType.
func busType(h windows.Handle) (uint32, error) {
	query := make([]byte, 12) // StorageDeviceProperty, PropertyStandardQuery
	out := make([]byte, 1024)
	n, err := ioctl(h, ioctlStorageQueryProperty, query, out)
	if err != nil {
		return 0, fmt.Errorf("IOCTL_STORAGE_QUERY_PROPERTY: %w", err)
	}
	if n < 32 {
		return 0, fmt.Errorf("IOCTL_STORAGE_QUERY_PROPERTY returned %d bytes", n)
	}
	return binary.LittleEndian.Uint32(out[28:]), nil
}

func diskLength(h windows.Handle) (int64, error) {
	out := make([]byte, 8)
	if _, err := ioctl(h, ioctlDiskGetLengthInfo, nil, out); err != nil {
		return 0, fmt.Errorf("IOCTL_DISK_GET_LENGTH_INFO: %w", err)
	}
	return int64(binary.LittleEndian.Uint64(out)), nil
}

func sectorSize(h windows.Handle) (int64, error) {
	out := make([]byte, 256)
	if _, err := ioctl(h, ioctlDiskGetDriveGeometryEx, nil, out); err != nil {
		return 0, fmt.Errorf("IOCTL_DISK_GET_DRIVE_GEOMETRY_EX: %w", err)
	}
	return int64(binary.LittleEndian.Uint32(out[20:])), nil
}

// diskNumber is STORAGE_DEVICE_NUMBER.DeviceNumber of a whole disk handle.
func diskNumber(h windows.Handle) (uint32, error) {
	out := make([]byte, 12)
	if _, err := ioctl(h, ioctlStorageGetDeviceNumber, nil, out); err != nil {
		return 0, fmt.Errorf("IOCTL_STORAGE_GET_DEVICE_NUMBER: %w", err)
	}
	if t := binary.LittleEndian.Uint32(out[0:]); t != fileDeviceDisk {
		return 0, fmt.Errorf("device type %#x is not a disk", t)
	}
	return binary.LittleEndian.Uint32(out[4:]), nil
}

// volumeDisks lists the disks a volume has extents on.
func volumeDisks(path string) ([]uint32, error) {
	h, err := openDevice(path, 0, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(h)
	out := make([]byte, 8+24*4)
	for {
		_, err := ioctl(h, ioctlVolumeGetVolumeDiskExtents, nil, out)
		if errors.Is(err, windows.ERROR_MORE_DATA) && len(out) < 1<<16 {
			out = make([]byte, 8+24*int(binary.LittleEndian.Uint32(out)))
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("IOCTL_VOLUME_GET_VOLUME_DISK_EXTENTS on %s: %w", path, err)
		}
		break
	}
	count := int(binary.LittleEndian.Uint32(out))
	if 8+24*count > len(out) {
		return nil, fmt.Errorf("%s reports %d extents", path, count)
	}
	disks := make([]uint32, count)
	for i := range disks {
		disks[i] = binary.LittleEndian.Uint32(out[8+24*i:])
	}
	return disks, nil
}

// volumes lists every volume as a \\?\Volume{GUID} device path, without
// the trailing backslash, so it opens the volume and not its root.
func volumes() ([]string, error) {
	buf := make([]uint16, windows.MAX_PATH)
	h, err := windows.FindFirstVolume(&buf[0], uint32(len(buf)))
	if err != nil {
		return nil, fmt.Errorf("FindFirstVolume: %w", err)
	}
	defer windows.FindVolumeClose(h)
	var out []string
	for {
		out = append(out, strings.TrimSuffix(windows.UTF16ToString(buf), `\`))
		if err := windows.FindNextVolume(h, &buf[0], uint32(len(buf))); err != nil {
			if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
				return out, nil
			}
			return nil, fmt.Errorf("FindNextVolume: %w", err)
		}
	}
}

// volumePathNames is where a volume is mounted: drive letters and folders.
func volumePathNames(volume string) ([]string, error) {
	name, err := windows.UTF16PtrFromString(volume + `\`)
	if err != nil {
		return nil, err
	}
	buf := make([]uint16, 1024)
	var n uint32
	if err := windows.GetVolumePathNamesForVolumeName(name, &buf[0], uint32(len(buf)), &n); err != nil {
		return nil, err
	}
	var out []string
	for _, s := range strings.Split(windows.UTF16ToString(buf[:n]), "\x00") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out, nil
}

// alignedBuf is n bytes starting on a 4096-byte boundary, which unbuffered
// disk I/O needs for any sector size up to 4 KiB.
func alignedBuf(n int) []byte {
	const align = 4096
	b := make([]byte, n+align)
	off := int(uintptr(unsafe.Pointer(&b[0])) & (align - 1))
	if off != 0 {
		off = align - off
	}
	return b[off : off+n : off+n]
}
