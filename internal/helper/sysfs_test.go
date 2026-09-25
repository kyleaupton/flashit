//go:build !windows

package helper

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fakeSys builds a sysfs tree: sys/devices/.../block/<disk>/<part> with
// partition markers, sys/dev/block/<maj:min> symlinks, and slaves links.
type fakeSys struct {
	t    *testing.T
	root string
}

func newFakeSys(t *testing.T) *fakeSys {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"dev/block", "block", "devices"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return &fakeSys{t: t, root: root}
}

func (s *fakeSys) devBlock() string { return filepath.Join(s.root, "dev", "block") }
func (s *fakeSys) block() string    { return filepath.Join(s.root, "block") }

// paths is the sysfs view of the fake: /dev/<name> resolves to itself and
// block/<name> plays /sys/class/block.
func (s *fakeSys) paths() sysfs {
	return sysfs{devBlock: s.devBlock(), classBlock: s.block(), resolve: func(p string) (string, error) { return p, nil }}
}

// disk creates devices/<disk> with the given partitions and links block/<disk>.
func (s *fakeSys) disk(name, majMin string, parts ...string) string {
	s.t.Helper()
	dir := filepath.Join(s.root, "devices", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		s.t.Fatal(err)
	}
	if err := os.Symlink(dir, filepath.Join(s.block(), name)); err != nil {
		s.t.Fatal(err)
	}
	if majMin != "" {
		if err := os.Symlink(dir, filepath.Join(s.devBlock(), majMin)); err != nil {
			s.t.Fatal(err)
		}
	}
	for _, p := range parts {
		pdir := filepath.Join(dir, p)
		if err := os.MkdirAll(pdir, 0o755); err != nil {
			s.t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pdir, "partition"), []byte("1\n"), 0o644); err != nil {
			s.t.Fatal(err)
		}
		// Partitions get a dev/block entry named <maj:min>.<part> in this fake,
		// and a class/block entry by name like the real sysfs.
		if err := os.Symlink(pdir, filepath.Join(s.devBlock(), majMin+"."+p)); err != nil {
			s.t.Fatal(err)
		}
		if err := os.Symlink(pdir, filepath.Join(s.block(), p)); err != nil {
			s.t.Fatal(err)
		}
	}
	return dir
}

func (s *fakeSys) slave(dev, slaveDir string) {
	s.t.Helper()
	dir := filepath.Join(s.root, "devices", dev, "slaves")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		s.t.Fatal(err)
	}
	if err := os.Symlink(slaveDir, filepath.Join(dir, filepath.Base(slaveDir))); err != nil {
		s.t.Fatal(err)
	}
}

func TestSystemDisksPlainRoot(t *testing.T) {
	sys := newFakeSys(t)
	sys.disk("nvme0n1", "259:0", "nvme0n1p1", "nvme0n1p2")
	sys.disk("sdb", "8:16", "sdb1")

	mounts := []mount{
		{majMin: "0:24", mountpoint: "/sys"},
		{majMin: "259:0.nvme0n1p2", mountpoint: "/"},
		{majMin: "259:0.nvme0n1p1", mountpoint: "/boot/efi"},
		{majMin: "8:16.sdb1", mountpoint: "/media/usb"},
	}
	got, err := systemDisksFrom(mounts, sys.paths())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"/dev/nvme0n1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestSystemDisksThroughDeviceMapper(t *testing.T) {
	sys := newFakeSys(t)
	nvme := sys.disk("nvme0n1", "259:0", "nvme0n1p3")
	sda := sys.disk("sda", "8:0", "sda1")
	sys.disk("dm-0", "253:0")
	sys.disk("dm-1", "253:1")
	// dm-1 (root) sits on dm-0 (LUKS), which spans two disks like an LVM VG.
	sys.slave("dm-1", filepath.Join(sys.root, "devices", "dm-0"))
	sys.slave("dm-0", filepath.Join(nvme, "nvme0n1p3"))
	sys.slave("dm-0", filepath.Join(sda, "sda1"))

	mounts := []mount{{majMin: "253:1", mountpoint: "/"}, {majMin: "8:0.sda1", mountpoint: "/home"}}
	got, err := systemDisksFrom(mounts, sys.paths())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"/dev/nvme0n1", "/dev/sda"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestSystemDisksFailClosed(t *testing.T) {
	sys := newFakeSys(t)
	sys.disk("sdb", "8:16", "sdb1")

	// Root on overlay (no block device) must be an error, not an empty list.
	if _, err := systemDisksFrom([]mount{{majMin: "0:40", mountpoint: "/", source: "overlay"}}, sys.paths()); err == nil {
		t.Fatal("expected error for a root that is not a block device")
	}
	if _, err := systemDisksFrom(nil, sys.paths()); err == nil {
		t.Fatal("expected error when / is missing")
	}
	// A secondary system mount that cannot be resolved is skipped, root still wins.
	got, err := systemDisksFrom([]mount{
		{majMin: "8:16", mountpoint: "/"},
		{majMin: "0:41", mountpoint: "/home", source: "tmpfs"},
	}, sys.paths())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"/dev/sdb"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

// btrfs (and ZFS) roots report an anonymous major 0; the mount source names
// the real device and must be followed.
func TestSystemDisksBtrfsRootViaSource(t *testing.T) {
	sys := newFakeSys(t)
	sys.disk("nvme0n1", "259:0", "nvme0n1p3")
	sys.disk("sdb", "8:16", "sdb1")

	mounts := []mount{
		{majMin: "0:38", mountpoint: "/", source: "/dev/nvme0n1p3"},
		{majMin: "0:38", mountpoint: "/home", source: "/dev/nvme0n1p3"},
		{majMin: "8:16.sdb1", mountpoint: "/media/usb", source: "/dev/sdb1"},
	}
	got, err := systemDisksFrom(mounts, sys.paths())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"/dev/nvme0n1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestSystemDisksOverlayRootStillRefuses(t *testing.T) {
	sys := newFakeSys(t)
	sys.disk("sdb", "8:16", "sdb1")
	for _, m := range []mount{
		{majMin: "0:40", mountpoint: "/", source: "overlay"},
		{majMin: "0:40", mountpoint: "/", source: "/dev/nope"},
		{majMin: "0:40", mountpoint: "/", source: "../../dev/sdb"},
	} {
		if _, err := systemDisksFrom([]mount{m}, sys.paths()); err == nil {
			t.Fatalf("root %+v should refuse", m)
		}
	}
}

func TestPartitionsOf(t *testing.T) {
	sys := newFakeSys(t)
	dir := sys.disk("sdb", "8:16", "sdb2", "sdb1")
	// Non-partition entries under the disk are ignored.
	if err := os.MkdirAll(filepath.Join(dir, "queue"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sdbx"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := partitionsOf(sys.block(), "sdb")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"/dev/sdb1", "/dev/sdb2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if _, err := partitionsOf(sys.block(), "sdz"); err == nil {
		t.Fatal("expected error for a missing disk")
	}
}

func TestParseMountInfo(t *testing.T) {
	in := `22 27 0:21 / /sys rw,nosuid - sysfs sysfs rw
27 1 259:2 / / rw,relatime - ext4 /dev/nvme0n1p2 rw
95 27 8:17 / /media/My\040USB rw - vfat /dev/sdb1 rw
40 1 0:38 /root / rw,relatime shared:1 - btrfs /dev/nvme0n1p3 rw,subvol=/root
41 1 0:39 / /overlay rw shared:2 master:1 - overlay overlay rw
broken line
`
	got, err := parseMountInfo(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := []mount{
		{"0:21", "/sys", "sysfs"},
		{"259:2", "/", "/dev/nvme0n1p2"},
		{"8:17", "/media/My USB", "/dev/sdb1"},
		{"0:38", "/", "/dev/nvme0n1p3"},
		{"0:39", "/overlay", "overlay"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestUnescapeMount(t *testing.T) {
	cases := map[string]string{
		"/plain":            "/plain",
		`/a\040b`:           "/a b",
		`/tab\011x`:         "/tab\tx",
		`/back\134slash`:    `/back\slash`,
		`/trailing\04`:      `/trailing\04`,
		`/not\zzzan-escape`: `/not\zzzan-escape`,
	}
	for in, want := range cases {
		if got := unescapeMount(in); got != want {
			t.Errorf("unescapeMount(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPartitionPath(t *testing.T) {
	cases := map[string]string{
		"/dev/sdb":     "/dev/sdb1",
		"/dev/vdb":     "/dev/vdb1",
		"/dev/nvme0n1": "/dev/nvme0n1p1",
		"/dev/mmcblk0": "/dev/mmcblk0p1",
		"/dev/loop0":   "/dev/loop0p1",
	}
	for in, want := range cases {
		if got := partitionPath(in, 1); got != want {
			t.Errorf("partitionPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsSystemMount(t *testing.T) {
	for _, mp := range []string{"/", "/boot", "/boot/efi", "/home", "/cdrom"} {
		if !isSystemMount(mp) {
			t.Errorf("%s should be a system mount", mp)
		}
	}
	for _, mp := range []string{"/media/usb", "/run/media/flashit/X", "/mnt", "/homework"} {
		if isSystemMount(mp) {
			t.Errorf("%s should not be a system mount", mp)
		}
	}
}
