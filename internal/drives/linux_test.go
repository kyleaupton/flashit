//go:build linux

package drives

import "testing"

// modernJSON mirrors `lsblk -J -b -o ...` on util-linux >= 2.37, where values
// are native JSON numbers/booleans. It contains: a USB stick with two
// partitions (one mounted), an internal NVMe system disk, a loop device, and a
// USB SSD that reports rm=false but hotplug=true.
const modernJSON = `{
   "blockdevices": [
      {"name":"loop0","size":4096,"type":"loop","tran":null,"rm":false,"hotplug":false,"ro":true,"model":null,"vendor":null,"serial":null,"mountpoint":"/snap/bare/5"},
      {"name":"nvme0n1","size":512110190592,"type":"disk","tran":"nvme","rm":false,"hotplug":false,"ro":false,"model":"Samsung SSD 980","vendor":null,"serial":"S1234","mountpoint":null,
         "children":[
            {"name":"nvme0n1p1","size":536870912,"type":"part","rm":false,"ro":false,"mountpoint":"/boot/efi"},
            {"name":"nvme0n1p2","size":511560000000,"type":"part","rm":false,"ro":false,"mountpoint":"/"}
         ]},
      {"name":"sdb","size":15376000000,"type":"disk","tran":"usb","rm":true,"hotplug":true,"ro":false,"model":"Ultra USB 3.0 ","vendor":"SanDisk ","serial":"4C530001","mountpoint":null,
         "children":[
            {"name":"sdb1","size":15000000000,"type":"part","rm":true,"ro":false,"mountpoint":"/media/user/USB"},
            {"name":"sdb2","size":300000000,"type":"part","rm":true,"ro":false,"mountpoint":null}
         ]},
      {"name":"sdc","size":1000204886016,"type":"disk","tran":"usb","rm":false,"hotplug":true,"ro":false,"model":"Portable SSD T7","vendor":"Samsung","serial":"S5678","mountpoint":null}
   ]
}`

// legacyJSON mirrors util-linux < 2.37, where every value is emitted as a
// string. Just the USB stick and the internal disk, enough to prove the
// type-tolerant unmarshalers work.
const legacyJSON = `{
   "blockdevices": [
      {"name":"nvme0n1","size":"512110190592","type":"disk","tran":"nvme","rm":"0","hotplug":"0","ro":"0","model":"Samsung SSD 980","mountpoint":null},
      {"name":"sdb","size":"15376000000","type":"disk","tran":"usb","rm":"1","hotplug":"1","ro":"0","model":"Ultra USB 3.0","vendor":"SanDisk","serial":"4C530001","mountpoint":null,
         "children":[{"name":"sdb1","size":"15000000000","type":"part","rm":"1","ro":"0","mountpoint":"/media/user/USB"}]}
   ]
}`

func parseDrives(t *testing.T, raw string) []Drive {
	t.Helper()
	devs, err := parseLsblk([]byte(raw))
	if err != nil {
		t.Fatalf("parseLsblk: %v", err)
	}
	return drivesFromDevices(devs)
}

func findDrive(ds []Drive, bsd string) (Drive, bool) {
	for _, d := range ds {
		if d.BSDName == bsd {
			return d, true
		}
	}
	return Drive{}, false
}

func TestListRemovable_Modern(t *testing.T) {
	ds := parseDrives(t, modernJSON)

	// Should include the USB stick (sdb) and the hotplug USB SSD (sdc),
	// but NOT the internal nvme disk or the loop device.
	if len(ds) != 2 {
		t.Fatalf("expected 2 drives, got %d: %+v", len(ds), ds)
	}
	if _, ok := findDrive(ds, "nvme0n1"); ok {
		t.Error("internal nvme disk should not be listed")
	}
	if _, ok := findDrive(ds, "loop0"); ok {
		t.Error("loop device should not be listed")
	}

	stick, ok := findDrive(ds, "sdb")
	if !ok {
		t.Fatal("expected sdb to be listed")
	}
	if stick.Device != "/dev/sdb" {
		t.Errorf("Device = %q, want /dev/sdb", stick.Device)
	}
	if stick.SizeBytes != 15376000000 {
		t.Errorf("SizeBytes = %d, want 15376000000", stick.SizeBytes)
	}
	if stick.Protocol != "USB" {
		t.Errorf("Protocol = %q, want USB", stick.Protocol)
	}
	if stick.Model != "Ultra USB 3.0" { // trailing space trimmed
		t.Errorf("Model = %q, want %q", stick.Model, "Ultra USB 3.0")
	}
	if stick.Vendor != "SanDisk" {
		t.Errorf("Vendor = %q, want SanDisk", stick.Vendor)
	}
	if !stick.IsRemovable {
		t.Error("stick should be removable")
	}
	if len(stick.Mountpoints) != 1 || stick.Mountpoints[0] != "/media/user/USB" {
		t.Errorf("Mountpoints = %v, want [/media/user/USB]", stick.Mountpoints)
	}

	// The USB SSD reports rm=false but hotplug=true: should still be listed.
	ssd, ok := findDrive(ds, "sdc")
	if !ok {
		t.Fatal("hotplug USB SSD (sdc) should be listed")
	}
	if ssd.IsRemovable {
		t.Error("sdc IsRemovable should reflect rm=false")
	}
}

func TestListRemovable_LegacyStringTypes(t *testing.T) {
	ds := parseDrives(t, legacyJSON)

	if len(ds) != 1 {
		t.Fatalf("expected 1 drive, got %d: %+v", len(ds), ds)
	}
	stick := ds[0]
	if stick.BSDName != "sdb" {
		t.Fatalf("expected sdb, got %q", stick.BSDName)
	}
	if stick.SizeBytes != 15376000000 {
		t.Errorf("SizeBytes = %d, want 15376000000 (string-typed parse)", stick.SizeBytes)
	}
	if !stick.IsRemovable {
		t.Error("rm=\"1\" should parse as removable")
	}
	if len(stick.Mountpoints) != 1 || stick.Mountpoints[0] != "/media/user/USB" {
		t.Errorf("Mountpoints = %v, want [/media/user/USB]", stick.Mountpoints)
	}
}

func TestReadOnlyAndNonUSBExcluded(t *testing.T) {
	const raw = `{"blockdevices":[
		{"name":"sdz","size":8000000000,"type":"disk","tran":"usb","rm":true,"hotplug":true,"ro":true,"mountpoint":null},
		{"name":"sda","size":8000000000,"type":"disk","tran":"sata","rm":false,"hotplug":false,"ro":false,"mountpoint":null}
	]}`
	ds := parseDrives(t, raw)
	if len(ds) != 0 {
		t.Fatalf("expected 0 drives (RO USB + internal SATA), got %d: %+v", len(ds), ds)
	}
}
