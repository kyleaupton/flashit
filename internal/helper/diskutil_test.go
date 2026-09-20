package helper

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fakeDiskutil answers `info -plist <dev>` and `list -plist` from testdata.
// Devices without a fixture get diskutil's real "Could not find disk"
// answer, which comes with exit status 1.
type fakeDiskutil struct {
	t     *testing.T
	infos map[string]string // device (with or without /dev/) -> fixture file
	list  string
	calls []string
}

func (f *fakeDiskutil) run(_ context.Context, args ...string) ([]byte, error) {
	f.t.Helper()
	f.calls = append(f.calls, strings.Join(args, " "))
	switch {
	case len(args) == 3 && args[0] == "info" && args[1] == "-plist":
		name, ok := f.infos[strings.TrimPrefix(args[2], "/dev/")]
		if !ok {
			return fixture(f.t, "info-missing.plist"), errors.New("exit status 1")
		}
		return fixture(f.t, name), nil
	case len(args) == 2 && args[0] == "list" && args[1] == "-plist":
		if f.list == "" {
			return nil, errors.New("exit status 1")
		}
		return fixture(f.t, f.list), nil
	}
	f.t.Fatalf("unexpected diskutil %v", args)
	return nil, nil
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// thisMac mirrors the fixtures: an APFS boot volume on disk0, a USB stick
// disk4 with a FAT32 partition and an APFS container disk5, a disk image
// disk6.
func thisMac(t *testing.T) (*fakeDiskutil, diskutil) {
	f := &fakeDiskutil{t: t, list: "list.plist", infos: map[string]string{
		"disk3s1s1": "info-root-volume.plist",
		"disk0s2":   "info-disk0s2.plist",
		"disk0":     "info-disk0.plist",
		"disk4":     "info-usb.plist",
		"disk4s1":   "info-usb-partition.plist",
		"disk4s2":   "info-usb-apfs-store.plist",
		"disk5":     "info-usb-container.plist",
		"disk6":     "info-disk-image.plist",
	}}
	return f, diskutil{run: f.run}
}

func TestDiskutilInfoErrors(t *testing.T) {
	_, du := thisMac(t)
	if _, err := du.info(context.Background(), "/dev/disk99"); err == nil || !strings.Contains(err.Error(), "Could not find disk") {
		t.Fatalf("missing disk: %v", err)
	}
	broken := diskutil{run: func(context.Context, ...string) ([]byte, error) { return []byte("not a plist"), nil }}
	if _, err := broken.info(context.Background(), "/dev/disk4"); err == nil {
		t.Fatal("garbage output must be an error")
	}
	empty := diskutil{run: func(context.Context, ...string) ([]byte, error) {
		return []byte(`<plist version="1.0"><dict/></plist>`), nil
	}}
	if _, err := empty.info(context.Background(), "/dev/disk4"); err == nil {
		t.Fatal("an answer without a device identifier must be an error")
	}
}

func TestDeviceInfoFrom(t *testing.T) {
	_, du := thisMac(t)
	ctx := context.Background()
	cases := []struct {
		dev  string
		want DeviceInfo
	}{
		{"/dev/disk4", DeviceInfo{Path: "/dev/disk4", IsBlock: true, WholeDisk: "/dev/disk4", Size: 31029460992, Removable: true}},
		{"/dev/disk4s1", DeviceInfo{Path: "/dev/disk4s1", IsBlock: true, WholeDisk: "/dev/disk4", Size: 31027363840, Removable: true}},
		{"/dev/disk0", DeviceInfo{Path: "/dev/disk0", IsBlock: true, WholeDisk: "/dev/disk0", Size: 500277792768, Removable: false}},
		{"/dev/disk0s2", DeviceInfo{Path: "/dev/disk0s2", IsBlock: true, WholeDisk: "/dev/disk0", Size: 494384795648, Removable: false}},
		// Synthesized containers and disk images say Ejectable and
		// RemovableMedia, and are Virtual; they are never a target.
		{"/dev/disk5", DeviceInfo{Path: "/dev/disk5", IsBlock: true, WholeDisk: "/dev/disk5", Size: 999997440, Removable: false}},
		{"/dev/disk6", DeviceInfo{Path: "/dev/disk6", IsBlock: true, WholeDisk: "/dev/disk6", Size: 9128936960, Removable: false}},
	}
	for _, c := range cases {
		inf, err := du.info(ctx, c.dev)
		if err != nil {
			t.Fatalf("%s: %v", c.dev, err)
		}
		got, err := deviceInfoFrom(c.dev, inf)
		if err != nil {
			t.Fatalf("%s: %v", c.dev, err)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Fatalf("%s: got %+v want %+v", c.dev, got, c.want)
		}
	}

	// diskutil answering about another device, or without a whole disk, is
	// refused rather than trusted.
	inf, _ := du.info(ctx, "/dev/disk4")
	if _, err := deviceInfoFrom("/dev/disk0", inf); err == nil {
		t.Fatal("answer about a different device was accepted")
	}
	inf.ParentWholeDisk = ""
	if _, err := deviceInfoFrom("/dev/disk4", inf); err == nil {
		t.Fatal("answer without a whole disk was accepted")
	}
	inf, _ = du.info(ctx, "/dev/disk4")
	inf.WholeDisk = false
	if _, err := deviceInfoFrom("/dev/disk4", inf); err == nil {
		t.Fatal("contradictory WholeDisk flag was accepted")
	}
}

func TestSystemDisks(t *testing.T) {
	ctx := context.Background()
	f, du := thisMac(t)
	got, err := du.systemDisks(ctx, "/dev/disk3s1s1")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"/dev/disk0"}) {
		t.Fatalf("system disks %v", got)
	}
	want := []string{"info -plist disk3s1s1", "info -plist disk0s2"}
	if !reflect.DeepEqual(f.calls, want) {
		t.Fatalf("calls %v, want %v", f.calls, want)
	}

	// A non-APFS root resolves straight to its whole disk.
	f, du = thisMac(t)
	f.infos["disk4s1"] = "info-usb-partition.plist"
	got, err = du.systemDisks(ctx, "/dev/disk4s1")
	if err != nil || !reflect.DeepEqual(got, []string{"/dev/disk4"}) {
		t.Fatalf("plain root: %v %v", got, err)
	}

	// Two physical stores (Fusion) both count.
	f, du = thisMac(t)
	f.infos["disk0s2"] = "info-usb-apfs-store.plist" // ParentWholeDisk disk4
	f.infos["disk3s1s1"] = "info-root-volume.plist"
	fusion := diskutil{run: func(ctx context.Context, args ...string) ([]byte, error) {
		if args[2] == "disk3s1s1" {
			return []byte(`<plist version="1.0"><dict>
				<key>DeviceIdentifier</key><string>disk3s1s1</string>
				<key>ParentWholeDisk</key><string>disk3</string>
				<key>APFSPhysicalStores</key><array>
					<dict><key>APFSPhysicalStore</key><string>disk0s2</string></dict>
					<dict><key>APFSPhysicalStore</key><string>disk4s2</string></dict>
				</array></dict></plist>`), nil
		}
		return f.run(ctx, args...)
	}}
	f.infos["disk0s2"] = "info-disk0s2.plist"
	got, err = fusion.systemDisks(ctx, "/dev/disk3s1s1")
	if err != nil || !reflect.DeepEqual(got, []string{"/dev/disk0", "/dev/disk4"}) {
		t.Fatalf("fusion: %v %v", got, err)
	}

	// Every failure refuses rather than guessing.
	for name, tc := range map[string]struct {
		root  string
		setup func(f *fakeDiskutil)
	}{
		"root not a device": {"devfs", nil},
		"root unknown":      {"/dev/disk99", nil},
		"store unknown":     {"/dev/disk3s1s1", func(f *fakeDiskutil) { delete(f.infos, "disk0s2") }},
		"store nameless": {"/dev/disk3s1s1", func(f *fakeDiskutil) {
			f.infos["disk3s1s1"] = "info-nameless-store.plist"
		}},
		"no whole disk": {"/dev/disk4s1", func(f *fakeDiskutil) { f.infos["disk4s1"] = "info-no-parent.plist" }},
	} {
		t.Run(name, func(t *testing.T) {
			f, du := thisMac(t)
			if tc.setup != nil {
				tc.setup(f)
			}
			got, err := du.systemDisks(ctx, tc.root)
			if err == nil {
				t.Fatalf("got %v, want an error", got)
			}
		})
	}
}

func TestContainersOn(t *testing.T) {
	ctx := context.Background()
	_, du := thisMac(t)

	containers, err := du.containersOn(ctx, "/dev/disk4")
	if err != nil || !reflect.DeepEqual(containers, []string{"/dev/disk5"}) {
		t.Fatalf("containers on disk4: %v %v", containers, err)
	}
	containers, err = du.containersOn(ctx, "/dev/disk0")
	if err != nil || !reflect.DeepEqual(containers, []string{"/dev/disk3"}) {
		t.Fatalf("containers on disk0: %v %v", containers, err)
	}
	// disk4 must not match disk40, and a disk with no container has none.
	containers, err = du.containersOn(ctx, "/dev/disk40")
	if err != nil || len(containers) != 0 {
		t.Fatalf("containers on disk40: %v %v", containers, err)
	}
}

func TestMountpoint(t *testing.T) {
	_, du := thisMac(t)
	mp, err := du.mountpoint(context.Background(), "/dev/disk4s1")
	if err != nil || mp != "/Volumes/ESD-USB" {
		t.Fatalf("mountpoint %q %v", mp, err)
	}
	mp, err = du.mountpoint(context.Background(), "/dev/disk4s2")
	if err != nil || mp != "" {
		t.Fatalf("unmounted partition: %q %v", mp, err)
	}
	if _, err := du.mountpoint(context.Background(), "/dev/disk99"); err == nil {
		t.Fatal("unknown partition must be an error")
	}
}
