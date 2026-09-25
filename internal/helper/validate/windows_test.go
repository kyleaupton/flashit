package validate

import (
	"errors"
	"testing"

	"github.com/kyleaupton/flashit/internal/proto"
)

func TestPhysicalDrivePath(t *testing.T) {
	cases := []struct {
		in   string
		n    uint32
		want proto.ErrorCode
	}{
		{`\\.\PhysicalDrive0`, 0, ""},
		{`\\.\PhysicalDrive1`, 1, ""},
		{`\\.\PhysicalDrive42`, 42, ""},
		{`\\.\PhysicalDrive4294967295`, 4294967295, ""},
		{`\\.\PhysicalDrive4294967296`, 0, proto.CodeInvalidDevice},
		{`\\.\PhysicalDrive99999999999`, 0, proto.CodeInvalidDevice},
		{``, 0, proto.CodeInvalidDevice},
		{`\\.\PhysicalDrive`, 0, proto.CodeInvalidDevice},
		{`\\.\PhysicalDrive01`, 0, proto.CodeInvalidDevice},
		{`\\.\PhysicalDrive1 `, 0, proto.CodeInvalidDevice},
		{`\\.\PhysicalDrive1\`, 0, proto.CodeInvalidDevice},
		{`\\.\PhysicalDrive1\..\PhysicalDrive0`, 0, proto.CodeInvalidDevice},
		{`\\.\PhysicalDrive1:`, 0, proto.CodeInvalidDevice},
		{`\\.\PhysicalDrive-1`, 0, proto.CodeInvalidDevice},
		{`\\.\PhysicalDrive+1`, 0, proto.CodeInvalidDevice},
		{`\\.\PhysicalDrive1x`, 0, proto.CodeInvalidDevice},
		{`\\.\PhysicalDrive１`, 0, proto.CodeInvalidDevice},
		{"\\\\.\\PhysicalDrive1\x00", 0, proto.CodeInvalidDevice},
		{`\\.\physicaldrive1`, 0, proto.CodeInvalidDevice},
		{`\\?\PhysicalDrive1`, 0, proto.CodeInvalidDevice},
		{`\\.\..\PhysicalDrive1`, 0, proto.CodeInvalidDevice},
		{`\\.\C:`, 0, proto.CodeInvalidDevice},
		{`\\.\Volume{3f1c2a8e-0000-0000-0000-100000000000}`, 0, proto.CodeInvalidDevice},
		{`\\?\Volume{3f1c2a8e-0000-0000-0000-100000000000}\`, 0, proto.CodeInvalidDevice},
		{`\\?\GLOBALROOT\Device\Harddisk1\DR1`, 0, proto.CodeInvalidDevice},
		{`\\.\GLOBALROOT\Device\Harddisk1\Partition0`, 0, proto.CodeInvalidDevice},
		{`C:\Windows`, 0, proto.CodeInvalidDevice},
		{`PhysicalDrive1`, 0, proto.CodeInvalidDevice},
		{`/dev/sdb`, 0, proto.CodeInvalidDevice},
		{` \\.\PhysicalDrive1`, 0, proto.CodeInvalidDevice},
	}
	for _, c := range cases {
		got, n, err := PhysicalDrivePath(c.in)
		wantCode(t, err, c.want)
		if c.want == "" && (got != c.in || n != c.n) {
			t.Fatalf("PhysicalDrivePath(%q) = %q, %d", c.in, got, n)
		}
	}
	if PhysicalDrive(7) != `\\.\PhysicalDrive7` {
		t.Fatal(PhysicalDrive(7))
	}
}

func TestRemovableBus(t *testing.T) {
	for bus, want := range map[uint32]bool{
		BusUSB: true, BusSD: true, BusMMC: true,
		BusVirtual: false, BusFileBackedVirtual: false,
		0x00: false, // unknown
		0x03: false, // ATA
		0x0b: false, // SATA
		0x11: false, // NVMe
		0x0a: false, // SAS
		0x08: false, // RAID
		0x10: false, // Spaces
		0xff: false,
	} {
		if RemovableBus(bus) != want {
			t.Errorf("RemovableBus(%#x) = %v", bus, !want)
		}
	}
}

// The Windows gate as the helper runs it: the path rule, then Target.
func TestWindowsTarget(t *testing.T) {
	winPath := func(p string) (string, error) {
		p, _, err := PhysicalDrivePath(p)
		return p, err
	}
	usb := DeviceInfo{Path: `\\.\PhysicalDrive2`, IsBlock: true, WholeDisk: `\\.\PhysicalDrive2`, Size: 32 << 30, Removable: RemovableBus(BusUSB)}
	vhd := usb
	vhd.Removable = RemovableBus(BusFileBackedVirtual)
	nvme := DeviceInfo{Path: `\\.\PhysicalDrive0`, IsBlock: true, WholeDisk: `\\.\PhysicalDrive0`, Size: 1 << 40, Removable: RemovableBus(0x11)}
	sysUSB := DeviceInfo{Path: `\\.\PhysicalDrive1`, IsBlock: true, WholeDisk: `\\.\PhysicalDrive1`, Size: 64 << 30, Removable: true}
	volume := DeviceInfo{Path: `\\.\E:`, IsBlock: true, WholeDisk: `\\.\E:`, Removable: true}
	sys := []string{`\\.\PhysicalDrive0`, `\\.\PhysicalDrive1`}

	cases := []struct {
		name string
		info DeviceInfo
		sys  []string
		want proto.ErrorCode
	}{
		{"usb stick", usb, sys, ""},
		{"vhd", vhd, sys, proto.CodeNotRemovable},
		{"internal nvme", nvme, sys, proto.CodeNotRemovable},
		{"windows to go stick", sysUSB, sys, proto.CodeSystemDisk},
		{"volume path", volume, sys, proto.CodeInvalidDevice},
		{"system disks unknown", usb, nil, proto.CodeSystemDisk},
		{"usb stick is the esp's disk", usb, []string{`\\.\PhysicalDrive0`, `\\.\PhysicalDrive2`}, proto.CodeSystemDisk},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wantCode(t, target(winPath, c.info, c.sys), c.want)
		})
	}
}

func TestSystemDisks(t *testing.T) {
	extents := map[string][]uint32{
		`\\.\C:`:                                {0},
		`\\?\GLOBALROOT\Device\HarddiskVolume1`: {0},
		`\\.\D:`:                                {1, 3}, // spanned
		`\\.\E:`:                                {},
	}
	disksOf := func(v string) ([]uint32, error) {
		d, ok := extents[v]
		if !ok {
			return nil, errors.New("no such volume")
		}
		return d, nil
	}

	got, err := SystemDisks([]string{`\\.\C:`, `\\?\GLOBALROOT\Device\HarddiskVolume1`, `\\.\D:`}, disksOf)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{`\\.\PhysicalDrive0`, `\\.\PhysicalDrive1`, `\\.\PhysicalDrive3`}
	if len(got) != len(want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %q, want %q", got, want)
		}
	}

	for name, vols := range map[string][]string{
		"no volumes":          nil,
		"unresolvable volume": {`\\.\C:`, `\\.\Z:`},
		"volume on no disk":   {`\\.\C:`, `\\.\E:`},
	} {
		if got, err := SystemDisks(vols, disksOf); err == nil {
			t.Errorf("%s: got %q, want an error", name, got)
		}
	}
}

func TestPageFileVolumes(t *testing.T) {
	got, err := PageFileVolumes([]string{
		`\??\C:\pagefile.sys`,
		`d:\pagefile.sys 0 0`,
		`?:\pagefile.sys`,
		``,
		`  E:\swap\pagefile.sys 1024 4096  `,
	}, 'C')
	if err != nil {
		t.Fatal(err)
	}
	want := []string{`C:\`, `D:\`, `E:\`}
	if len(got) != len(want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
	if got, err := PageFileVolumes(nil, 'C'); err != nil || len(got) != 0 {
		t.Fatalf("no page files: %q, %v", got, err)
	}
	for _, bad := range []string{
		`pagefile.sys`,
		`\Device\HarddiskVolume3\pagefile.sys`,
		`\\?\Volume{0}\pagefile.sys`,
		`1:\pagefile.sys`,
		`C:pagefile.sys`,
		`\??\UNC\server\share\pagefile.sys`,
	} {
		if got, err := PageFileVolumes([]string{`C:\pagefile.sys`, bad}, 'C'); err == nil {
			t.Errorf("%q: got %q, want an error", bad, got)
		}
	}
	if _, err := PageFileVolumes([]string{`?:\pagefile.sys`}, 0); err == nil {
		t.Error("? without a system drive was accepted")
	}
}
