package helper

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// The sysfs walking below has no Linux-only calls so it can be tested against
// a fake tree on any host; disk_linux.go points it at the real /sys.

// systemMounts are the mountpoints whose backing disks are never a target.
var systemMounts = []string{"/", "/boot", "/boot/efi", "/efi", "/usr", "/var", "/home", "/cdrom"}

func isSystemMount(mp string) bool {
	for _, s := range systemMounts {
		if mp == s {
			return true
		}
	}
	return false
}

type mount struct {
	majMin     string
	mountpoint string
}

// parseMountInfo reads /proc/self/mountinfo lines: field 3 is major:minor,
// field 5 the mountpoint.
func parseMountInfo(r io.Reader) ([]mount, error) {
	var out []mount
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 5 {
			continue
		}
		out = append(out, mount{majMin: fields[2], mountpoint: unescapeMount(fields[4])})
	}
	return out, sc.Err()
}

// unescapeMount decodes the octal escapes mountinfo uses for space, tab,
// newline and backslash.
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if v, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(v))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// systemDisksFrom resolves every system mount to the whole disks behind it.
// "/" must resolve or the result is an error, so an unknown root refuses
// everything rather than guessing.
func systemDisksFrom(mounts []mount, sysDevBlock string) ([]string, error) {
	seen := map[string]bool{}
	var disks []string
	rootFound := false
	for _, m := range mounts {
		if !isSystemMount(m.mountpoint) {
			continue
		}
		sysPath, err := filepath.EvalSymlinks(filepath.Join(sysDevBlock, m.majMin))
		if err == nil {
			var wholes []string
			wholes, err = wholeDisksForSysPath(sysPath, 0)
			if err == nil {
				if m.mountpoint == "/" {
					rootFound = true
				}
				for _, w := range wholes {
					if !seen[w] {
						seen[w] = true
						disks = append(disks, w)
					}
				}
			}
		}
		if err != nil && m.mountpoint == "/" {
			return nil, fmt.Errorf("resolve device backing /: %w", err)
		}
	}
	if !rootFound {
		return nil, errors.New("/ is not backed by a block device")
	}
	sort.Strings(disks)
	return disks, nil
}

// wholeDisksForSysPath follows a resolved sysfs block entry through its
// partition parent and device-mapper or md slaves down to physical disks.
func wholeDisksForSysPath(sysPath string, depth int) ([]string, error) {
	if depth > 8 {
		return nil, errors.New("device stack too deep")
	}
	if _, err := os.Stat(filepath.Join(sysPath, "partition")); err == nil {
		sysPath = filepath.Dir(sysPath)
	}
	slaves, _ := os.ReadDir(filepath.Join(sysPath, "slaves"))
	if len(slaves) == 0 {
		return []string{"/dev/" + filepath.Base(sysPath)}, nil
	}
	var out []string
	for _, s := range slaves {
		target, err := filepath.EvalSymlinks(filepath.Join(sysPath, "slaves", s.Name()))
		if err != nil {
			return nil, err
		}
		more, err := wholeDisksForSysPath(target, depth+1)
		if err != nil {
			return nil, err
		}
		out = append(out, more...)
	}
	return out, nil
}

// partitionsOf lists the partition nodes under a whole disk's sysfs entry.
func partitionsOf(sysBlock, name string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(sysBlock, name))
	if err != nil {
		return nil, err
	}
	var parts []string
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), name) {
			continue
		}
		if _, err := os.Stat(filepath.Join(sysBlock, name, e.Name(), "partition")); err == nil {
			parts = append(parts, "/dev/"+e.Name())
		}
	}
	sort.Strings(parts)
	return parts, nil
}

// partitionPath follows the kernel's rule: a 'p' separator when the disk name
// ends in a digit (nvme0n1p1, mmcblk0p1), none otherwise (sdb1).
func partitionPath(device string, n int) string {
	last := device[len(device)-1]
	if last >= '0' && last <= '9' {
		return fmt.Sprintf("%sp%d", device, n)
	}
	return fmt.Sprintf("%s%d", device, n)
}

func readUint(path string) (uint64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
}
