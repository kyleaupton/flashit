package helper

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"howett.net/plist"
)

// The diskutil handling below has no macOS-only calls so it can be tested
// against canned plists on any host; disk_darwin.go supplies the real exec.

// diskutil reads the subset of `diskutil info -plist` and `diskutil list
// -plist` the helper needs. run executes diskutil with the given arguments
// and returns its stdout even when it exits non-zero, since diskutil reports
// errors as a plist.
type diskutil struct {
	run func(ctx context.Context, args ...string) ([]byte, error)
}

type duInfo struct {
	Error              bool   `plist:"Error"`
	ErrorMessage       string `plist:"ErrorMessage"`
	DeviceIdentifier   string `plist:"DeviceIdentifier"`
	WholeDisk          bool   `plist:"WholeDisk"`
	ParentWholeDisk    string `plist:"ParentWholeDisk"`
	RemovableMedia     bool   `plist:"RemovableMedia"`
	Ejectable          bool   `plist:"Ejectable"`
	Internal           bool   `plist:"Internal"`
	VirtualOrPhysical  string `plist:"VirtualOrPhysical"`
	TotalSize          uint64 `plist:"TotalSize"`
	Size               uint64 `plist:"Size"`
	MountPoint         string `plist:"MountPoint"`
	APFSPhysicalStores []struct {
		Store string `plist:"APFSPhysicalStore"`
	} `plist:"APFSPhysicalStores"`
}

type duListDisk struct {
	DeviceIdentifier string `plist:"DeviceIdentifier"`
	Partitions       []struct {
		DeviceIdentifier string `plist:"DeviceIdentifier"`
	} `plist:"Partitions"`
	APFSPhysicalStores []struct {
		DeviceIdentifier string `plist:"DeviceIdentifier"`
	} `plist:"APFSPhysicalStores"`
}

type duList struct {
	AllDisksAndPartitions []duListDisk `plist:"AllDisksAndPartitions"`
}

func (d diskutil) info(ctx context.Context, dev string) (duInfo, error) {
	out, runErr := d.run(ctx, "info", "-plist", dev)
	var inf duInfo
	if _, err := plist.Unmarshal(out, &inf); err != nil {
		if runErr != nil {
			return duInfo{}, fmt.Errorf("diskutil info %s: %w", dev, runErr)
		}
		return duInfo{}, fmt.Errorf("parse diskutil info %s: %w", dev, err)
	}
	if inf.Error {
		return duInfo{}, fmt.Errorf("diskutil info %s: %s", dev, inf.ErrorMessage)
	}
	if runErr != nil {
		return duInfo{}, fmt.Errorf("diskutil info %s: %w", dev, runErr)
	}
	if inf.DeviceIdentifier == "" {
		return duInfo{}, fmt.Errorf("diskutil info %s: no device identifier", dev)
	}
	return inf, nil
}

func (d diskutil) list(ctx context.Context) (duList, error) {
	out, err := d.run(ctx, "list", "-plist")
	if err != nil {
		return duList{}, fmt.Errorf("diskutil list: %w", err)
	}
	var lst duList
	if _, err := plist.Unmarshal(out, &lst); err != nil {
		return duList{}, fmt.Errorf("parse diskutil list: %w", err)
	}
	return lst, nil
}

// deviceInfoFrom turns a diskutil answer about resolved into a DeviceInfo.
// The answer must be about the device asked for, and must name its whole
// disk, or the device is unusable. Disk images and synthesized APFS
// containers report themselves as Virtual and are never removable media.
func deviceInfoFrom(resolved string, inf duInfo) (DeviceInfo, error) {
	if "/dev/"+inf.DeviceIdentifier != resolved {
		return DeviceInfo{}, fmt.Errorf("diskutil answered about %s, not %s", inf.DeviceIdentifier, resolved)
	}
	if inf.ParentWholeDisk == "" {
		return DeviceInfo{}, fmt.Errorf("diskutil reports no whole disk for %s", resolved)
	}
	whole := "/dev/" + inf.ParentWholeDisk
	if inf.WholeDisk != (whole == resolved) {
		return DeviceInfo{}, fmt.Errorf("diskutil reports WholeDisk=%v but parent %s for %s", inf.WholeDisk, whole, resolved)
	}
	size := inf.TotalSize
	if size == 0 {
		size = inf.Size
	}
	return DeviceInfo{
		Path:      resolved,
		IsBlock:   true,
		WholeDisk: whole,
		Size:      size,
		Removable: !inf.Internal && (inf.RemovableMedia || inf.Ejectable) && inf.VirtualOrPhysical != "Virtual",
	}, nil
}

// systemDisks resolves the device mounted at / to the physical whole disks
// behind it. The root of a macOS 13+ install is an APFS volume, so the
// volume's container lists its physical stores and each store's whole disk
// is a system disk. Anything missing on the way is an error, never an empty
// answer.
func (d diskutil) systemDisks(ctx context.Context, rootDev string) ([]string, error) {
	if !strings.HasPrefix(rootDev, "/dev/") {
		return nil, fmt.Errorf("/ is mounted from %q, not a device", rootDev)
	}
	disks, err := d.wholeDisksBehind(ctx, filepath.Base(rootDev), 0)
	if err != nil {
		return nil, fmt.Errorf("resolve disks behind /: %w", err)
	}
	if len(disks) == 0 {
		return nil, errors.New("resolve disks behind /: none found")
	}
	sort.Strings(disks)
	return disks, nil
}

func (d diskutil) wholeDisksBehind(ctx context.Context, dev string, depth int) ([]string, error) {
	if depth > 4 {
		return nil, errors.New("device stack too deep")
	}
	inf, err := d.info(ctx, dev)
	if err != nil {
		return nil, err
	}
	if len(inf.APFSPhysicalStores) == 0 {
		if inf.ParentWholeDisk == "" {
			return nil, fmt.Errorf("%s has no whole disk", dev)
		}
		return []string{"/dev/" + inf.ParentWholeDisk}, nil
	}
	seen := map[string]bool{}
	var out []string
	for _, s := range inf.APFSPhysicalStores {
		if s.Store == "" {
			return nil, fmt.Errorf("%s has an unnamed physical store", dev)
		}
		more, err := d.wholeDisksBehind(ctx, s.Store, depth+1)
		if err != nil {
			return nil, err
		}
		for _, w := range more {
			if !seen[w] {
				seen[w] = true
				out = append(out, w)
			}
		}
	}
	return out, nil
}

// containersOn lists the synthesized APFS containers whose physical store
// sits on whole. They hold the disk busy until unmounted, so unmounting a
// physical disk means unmounting them first.
func (d diskutil) containersOn(ctx context.Context, whole string) ([]string, error) {
	lst, err := d.list(ctx)
	if err != nil {
		return nil, err
	}
	name := filepath.Base(whole)
	var out []string
	for _, disk := range lst.AllDisksAndPartitions {
		for _, s := range disk.APFSPhysicalStores {
			if s.DeviceIdentifier == name || strings.HasPrefix(s.DeviceIdentifier, name+"s") {
				out = append(out, "/dev/"+disk.DeviceIdentifier)
				break
			}
		}
	}
	return out, nil
}

func (d diskutil) mountpoint(ctx context.Context, dev string) (string, error) {
	inf, err := d.info(ctx, dev)
	if err != nil {
		return "", err
	}
	return inf.MountPoint, nil
}
