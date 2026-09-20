package validate

import (
	"errors"
	"testing"

	"github.com/kyleaupton/flashit/internal/proto"
)

func wantCode(t *testing.T, err error, code proto.ErrorCode) {
	t.Helper()
	if code == "" {
		if err != nil {
			t.Fatalf("expected success, got %v", err)
		}
		return
	}
	var pe *proto.Error
	if !errors.As(err, &pe) {
		t.Fatalf("expected *proto.Error %s, got %v", code, err)
	}
	if pe.Code != code {
		t.Fatalf("expected code %s, got %s (%s)", code, pe.Code, pe.Message)
	}
}

func TestDevicePath(t *testing.T) {
	cases := []struct {
		in   string
		want proto.ErrorCode
	}{
		{"/dev/sdb", ""},
		{"/dev/nvme0n1", ""},
		{"/dev/disk/by-id/usb-Kingston", ""},
		{"", proto.CodeInvalidDevice},
		{"sdb", proto.CodeInvalidDevice},
		{"dev/sdb", proto.CodeInvalidDevice},
		{"/dev/../etc/shadow", proto.CodeInvalidDevice},
		{"/dev/sdb/../sda", proto.CodeInvalidDevice},
		{"/dev/./sdb", proto.CodeInvalidDevice},
		{"/dev//sdb", proto.CodeInvalidDevice},
		{"/dev/sdb/", proto.CodeInvalidDevice},
		{"/dev", proto.CodeInvalidDevice},
		{"/dev/", proto.CodeInvalidDevice},
		{"/etc/shadow", proto.CodeInvalidDevice},
		{"/devices/sdb", proto.CodeInvalidDevice},
		{"/dev/sdb\x00", proto.CodeInvalidDevice},
	}
	for _, c := range cases {
		got, err := DevicePath(c.in)
		wantCode(t, err, c.want)
		if c.want == "" && got != c.in {
			t.Fatalf("DevicePath(%q) = %q", c.in, got)
		}
	}
}

func TestTarget(t *testing.T) {
	ok := DeviceInfo{Path: "/dev/sdb", IsBlock: true, WholeDisk: "/dev/sdb", Size: 1 << 30, Removable: true}
	sys := []string{"/dev/nvme0n1"}

	cases := []struct {
		name string
		info DeviceInfo
		sys  []string
		want proto.ErrorCode
	}{
		{"removable whole disk", ok, sys, ""},
		{"symlink resolved outside /dev", DeviceInfo{Path: "/etc/shadow", IsBlock: true, WholeDisk: "/etc/shadow", Removable: true}, sys, proto.CodeInvalidDevice},
		{"not a block device", DeviceInfo{Path: "/dev/null", IsBlock: false, WholeDisk: "/dev/null", Removable: true}, sys, proto.CodeInvalidDevice},
		{"partition of a removable disk", DeviceInfo{Path: "/dev/sdb1", IsBlock: true, WholeDisk: "/dev/sdb", Removable: true}, sys, proto.CodeInvalidDevice},
		{"partition of the system disk", DeviceInfo{Path: "/dev/nvme0n1p2", IsBlock: true, WholeDisk: "/dev/nvme0n1", Removable: true}, sys, proto.CodeInvalidDevice},
		{"internal disk", DeviceInfo{Path: "/dev/sda", IsBlock: true, WholeDisk: "/dev/sda", Removable: false}, sys, proto.CodeNotRemovable},
		{"system disk", DeviceInfo{Path: "/dev/nvme0n1", IsBlock: true, WholeDisk: "/dev/nvme0n1", Removable: true}, sys, proto.CodeSystemDisk},
		{"system disk among several", ok, []string{"/dev/nvme0n1", "/dev/sdb"}, proto.CodeSystemDisk},
		{"system disk unknown", ok, nil, proto.CodeSystemDisk},
		{"system disk empty string", ok, []string{""}, proto.CodeSystemDisk},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wantCode(t, Target(c.info, c.sys), c.want)
		})
	}
}

func TestSourcePath(t *testing.T) {
	cases := []struct {
		in   string
		want proto.ErrorCode
	}{
		{"/tmp/x.iso", ""},
		{"", proto.CodeInvalidSource},
		{"x.iso", proto.CodeInvalidSource},
		{"/tmp/../etc/shadow", proto.CodeInvalidSource},
		{"/tmp//x.iso", proto.CodeInvalidSource},
		{"/tmp/x.iso\x00", proto.CodeInvalidSource},
	}
	for _, c := range cases {
		_, err := SourcePath(c.in)
		wantCode(t, err, c.want)
	}
}

func TestCapacity(t *testing.T) {
	dev := DeviceInfo{Path: "/dev/sdb", Size: 1000}
	wantCode(t, Capacity(1000, dev), "")
	wantCode(t, Capacity(1, dev), "")
	wantCode(t, Capacity(1001, dev), proto.CodeInsufficientCapacity)
	wantCode(t, Capacity(-1, dev), proto.CodeInsufficientCapacity)
	wantCode(t, Capacity(1, DeviceInfo{Path: "/dev/sdb"}), proto.CodeInsufficientCapacity)
}

func TestLabel(t *testing.T) {
	cases := []struct {
		in   string
		want proto.ErrorCode
	}{
		{"FLASHIT", ""},
		{"a", ""},
		{"My Disk-1_", ""},
		{"ABCDEFGHIJK", ""},
		{"", proto.CodeInvalidLabel},
		{"ABCDEFGHIJKL", proto.CodeInvalidLabel},
		{"BAD;rm -rf", proto.CodeInvalidLabel},
		{"a/b", proto.CodeInvalidLabel},
		{"a.b", proto.CodeInvalidLabel},
		{"$(id)", proto.CodeInvalidLabel},
		{"ünïcode", proto.CodeInvalidLabel},
		{"a\nb", proto.CodeInvalidLabel},
	}
	for _, c := range cases {
		wantCode(t, Label(c.in), c.want)
	}
}

func TestFilesystem(t *testing.T) {
	for _, in := range []string{"fat32", "FAT32", "Fat32"} {
		got, err := Filesystem(in)
		wantCode(t, err, "")
		if got != "fat32" {
			t.Fatalf("Filesystem(%q) = %q", in, got)
		}
	}
	for _, in := range []string{"", "ntfs", "exfat", "ext4", "fat", "vfat"} {
		_, err := Filesystem(in)
		wantCode(t, err, proto.CodeInvalidRequest)
	}
}

func TestMountpoint(t *testing.T) {
	cases := []struct {
		in   string
		want proto.ErrorCode
	}{
		{MountRoot + "/FLASHIT", ""},
		{MountRoot + "/My Disk", ""},
		{"", proto.CodeInvalidRequest},
		{"FLASHIT", proto.CodeInvalidRequest},
		{MountRoot, proto.CodeInvalidRequest},
		{MountRoot + "/", proto.CodeInvalidRequest},
		{MountRoot + "/../../../etc", proto.CodeInvalidRequest},
		{MountRoot + "/FLASHIT/sub", proto.CodeInvalidRequest},
		{MountRoot + "/TOO-LONG-LABEL", proto.CodeInvalidRequest},
		{"/run/media/other/FLASHIT", proto.CodeInvalidRequest},
		{"/", proto.CodeInvalidRequest},
		{"/home", proto.CodeInvalidRequest},
	}
	for _, c := range cases {
		_, err := Mountpoint(c.in)
		wantCode(t, err, c.want)
	}
}
