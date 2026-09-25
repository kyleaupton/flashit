package validate

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/kyleaupton/flashit/internal/proto"
)

// The Windows gate. It lives here, without a build tag, so its tables run on
// every host.

const physicalDrivePrefix = `\\.\PhysicalDrive`

// PhysicalDrive is the canonical path of disk n.
func PhysicalDrive(n uint32) string { return physicalDrivePrefix + strconv.FormatUint(uint64(n), 10) }

// PhysicalDrivePath accepts exactly \\.\PhysicalDriveN, N a disk number in
// decimal without leading zeros, and returns it with N. Volume paths, \\?\
// paths, other casings and anything trailing are refused.
func PhysicalDrivePath(path string) (string, uint32, error) {
	digits, ok := strings.CutPrefix(path, physicalDrivePrefix)
	if !ok || digits == "" || len(digits) > 10 || (len(digits) > 1 && digits[0] == '0') {
		return "", 0, proto.Errorf(proto.CodeInvalidDevice, "device path %q is not \\\\.\\PhysicalDriveN", path)
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return "", 0, proto.Errorf(proto.CodeInvalidDevice, "device path %q is not \\\\.\\PhysicalDriveN", path)
		}
	}
	n, err := strconv.ParseUint(digits, 10, 32)
	if err != nil {
		return "", 0, proto.Errorf(proto.CodeInvalidDevice, "disk number in %q is out of range", path)
	}
	return path, uint32(n), nil
}

// STORAGE_BUS_TYPE values the gate cares about.
const (
	BusUSB               = 0x07
	BusSD                = 0x0c
	BusMMC               = 0x0d
	BusVirtual           = 0x0e
	BusFileBackedVirtual = 0x0f
)

// RemovableBus reports whether a disk on bus counts as removable: USB, SD or
// MMC. Everything else, virtual and file-backed virtual disks (VHDs)
// included, is refused.
func RemovableBus(bus uint32) bool {
	return bus == BusUSB || bus == BusSD || bus == BusMMC
}

// SystemDisks maps every volume that backs the running system to the disks
// it spans. It fails closed: no volumes, a volume it cannot resolve, or one
// that resolves to no disk is an error, never a shorter list.
func SystemDisks(volumes []string, disksOf func(volume string) ([]uint32, error)) ([]string, error) {
	if len(volumes) == 0 {
		return nil, fmt.Errorf("no system volumes found")
	}
	seen := map[uint32]bool{}
	var out []string
	for _, v := range volumes {
		disks, err := disksOf(v)
		if err != nil {
			return nil, fmt.Errorf("disks of %s: %w", v, err)
		}
		if len(disks) == 0 {
			return nil, fmt.Errorf("%s is on no disk", v)
		}
		for _, d := range disks {
			if !seen[d] {
				seen[d] = true
				out = append(out, PhysicalDrive(d))
			}
		}
	}
	return out, nil
}

// PageFileVolumes turns the registry's page file lists (ExistingPageFiles,
// like `\??\C:\pagefile.sys`, and PagingFiles, like `C:\pagefile.sys 0 0` or
// `?:\pagefile.sys`) into volume roots like `C:\`. "?" is the system drive,
// passed as a letter. An entry it cannot read is an error: a page file on an
// unknown volume could be on the stick.
func PageFileVolumes(entries []string, systemDrive byte) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		e = strings.TrimPrefix(e, `\??\`)
		if len(e) < 3 || e[1] != ':' || e[2] != '\\' {
			return nil, fmt.Errorf("page file entry %q has no drive letter", e)
		}
		letter := e[0]
		if letter == '?' {
			letter = systemDrive
		}
		if 'a' <= letter && letter <= 'z' {
			letter -= 'a' - 'A'
		}
		if letter < 'A' || letter > 'Z' {
			return nil, fmt.Errorf("page file entry %q has no drive letter", e)
		}
		root := string(letter) + `:\`
		if !seen[root] {
			seen[root] = true
			out = append(out, root)
		}
	}
	return out, nil
}
