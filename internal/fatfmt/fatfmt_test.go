package fatfmt

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	kib = int64(1) << 10
	mib = int64(1) << 20
	gib = int64(1) << 30
)

func sweepSizes(sector int64) []int64 {
	var sizes []int64
	for _, g := range []int64{1, 8, 16, 32, 33, 64, 128, 256} {
		sizes = append(sizes, g*gib)
	}
	// Not a multiple of 1 MiB.
	sizes = append(sizes, gib+3*sector, 5*gib+mib/2+sector)
	// A few sectors either side of each of go-diskfs's cluster-size steps,
	// which it takes on the partition size.
	for _, b := range []int64{260 * mib, 8 * gib, 16 * gib, 32 * gib} {
		for d := int64(-2); d <= 2; d++ {
			sizes = append(sizes, b+PartitionStart+d*sector)
		}
	}
	return sizes
}

func TestFormatSweep(t *testing.T) {
	for _, sector := range []int64{512, 4096} {
		for _, size := range sweepSizes(sector) {
			t.Run(fmt.Sprintf("%d/%d", sector, size), func(t *testing.T) {
				t.Parallel()
				img := formatImage(t, size, sector, "FLASHIT")
				checkImage(t, img, size, sector, "FLASHIT")
				fsck(t, img, size, sector)
			})
		}
	}
}

// go-diskfs v1.9.4 kept sectors per FAT in a uint16, which wrapped from
// 257 GiB up; these sizes are past that.
func TestFormatLarge(t *testing.T) {
	if testing.Short() {
		t.Skip("allocates a FAT of up to 128 MiB")
	}
	for _, sector := range []int64{512, 4096} {
		for _, size := range []int64{257 * gib, 512 * gib, 1 << 40} {
			t.Run(fmt.Sprintf("%d/%d", sector, size), func(t *testing.T) {
				img := formatImage(t, size, sector, "LARGE")
				checkImage(t, img, size, sector, "LARGE")
				fsck(t, img, size, sector)
			})
		}
	}
}

func TestFormatRefuses(t *testing.T) {
	f := sparseFile(t, 64*mib)
	for _, tc := range []struct {
		name         string
		size, sector int64
		label        string
		want         string
	}{
		{"above 2 TiB", 2<<40 + 4096, 4096, "X", "MBR cannot address"},
		{"1024-byte sectors", gib, 1024, "X", "sector size"},
		{"2048-byte sectors", gib, 2048, "X", "sector size"},
		{"520-byte sectors", gib, 520, "X", "sector size"},
		{"zero sectors", gib, 0, "X", "sector size"},
		{"too small", 16 * mib, 512, "X", "too small"},
		{"smaller than the partition start", 512 * kib, 512, "X", "too small"},
		{"empty label", gib, 512, "", "label"},
		{"long label", gib, 512, "ABCDEFGHIJKL", "label"},
		{"non-ASCII label", gib, 512, "ÜSB", "label"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := Format(f, tc.size, tc.sector, tc.label)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want an error about %q", err, tc.want)
			}
		})
	}
	// Refused before anything was written.
	buf := make([]byte, 1024)
	if _, err := f.ReadAt(buf, 0); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf, make([]byte, len(buf))) {
		t.Fatal("a refused format wrote to the device")
	}
}

// Up to 2 TiB at 4096-byte sectors fits go-diskfs's FAT size.
func TestLayoutAt4Kn(t *testing.T) {
	if err := checkLayout(2<<40-PartitionStart, 4096); err != nil {
		t.Fatal(err)
	}
}

func TestFormatWipesOldLayout(t *testing.T) {
	size := gib
	f := sparseFile(t, size)
	junk := bytes.Repeat([]byte("EFI PART"), 64)
	for _, off := range []int64{512, 40 * kib, size - 512} {
		if _, err := f.WriteAt(junk[:512], off); err != nil {
			t.Fatal(err)
		}
	}
	if err := Format(f, size, 512, "WIPED"); err != nil {
		t.Fatal(err)
	}
	for _, off := range []int64{512, 40 * kib, size - 512} {
		got := make([]byte, 512)
		if _, err := f.ReadAt(got, off); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, make([]byte, 512)) {
			t.Fatalf("old data left at %d", off)
		}
	}
}

// A panic inside go-diskfs comes back as an error.
func TestFormatRecoversPanic(t *testing.T) {
	err := Format(panicDevice{}, gib, 512, "X")
	if err == nil || !strings.Contains(err.Error(), "panicked") {
		t.Fatalf("got %v", err)
	}
}

type panicDevice struct{}

func (panicDevice) ReadAt(p []byte, off int64) (int, error)  { panic("boom") }
func (panicDevice) WriteAt(p []byte, off int64) (int, error) { panic("boom") }

// Every access reaching the device is whole sectors, as a raw Windows disk
// handle demands.
func TestFormatWritesWholeSectors(t *testing.T) {
	for _, sector := range []int64{512, 4096} {
		f := sparseFile(t, gib)
		d := &alignCheck{f: f, sector: sector}
		if err := Format(d, gib, sector, "ALIGN"); err != nil {
			t.Fatal(err)
		}
		if d.bad != "" {
			t.Fatalf("%d-byte sectors: %s", sector, d.bad)
		}
	}
}

type alignCheck struct {
	f      *os.File
	sector int64
	bad    string
}

func (a *alignCheck) check(op string, n int, off int64) {
	if a.bad == "" && (off%a.sector != 0 || int64(n)%a.sector != 0) {
		a.bad = fmt.Sprintf("%s of %d bytes at %d", op, n, off)
	}
}

func (a *alignCheck) ReadAt(p []byte, off int64) (int, error) {
	a.check("read", len(p), off)
	return a.f.ReadAt(p, off)
}

func (a *alignCheck) WriteAt(p []byte, off int64) (int, error) {
	a.check("write", len(p), off)
	return a.f.WriteAt(p, off)
}

func formatImage(t *testing.T, size, sector int64, label string) *os.File {
	t.Helper()
	f := sparseFile(t, size)
	if err := Format(f, size, sector, label); err != nil {
		t.Fatalf("format %d bytes at %d: %v", size, sector, err)
	}
	return f
}

func sparseFile(t *testing.T, size int64) *os.File {
	t.Helper()
	f, err := os.Create(filepath.Join(t.TempDir(), "disk.img"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	makeSparse(t, f)
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	return f
}

// checkImage reads the MBR and the FAT32 volume back and holds them to
// fatgen103, independently of go-diskfs.
func checkImage(t *testing.T, f *os.File, size, sector int64, label string) {
	t.Helper()
	read := func(off, n int64) []byte {
		b := make([]byte, n)
		if _, err := f.ReadAt(b, off); err != nil {
			t.Fatalf("read %d at %d: %v", n, off, err)
		}
		return b
	}
	le16 := binary.LittleEndian.Uint16
	le32 := binary.LittleEndian.Uint32

	mbrSec := read(0, 512)
	if mbrSec[510] != 0x55 || mbrSec[511] != 0xaa {
		t.Fatal("no MBR signature")
	}
	if le32(mbrSec[440:]) == 0 {
		t.Fatal("disk signature is zero")
	}
	e := mbrSec[446:462]
	startLBA := uint32(PartitionStart / sector)
	partSectors := uint32(size/sector) - startLBA
	if e[0] != 0x80 || e[4] != 0x0c || le32(e[8:]) != startLBA || le32(e[12:]) != partSectors {
		t.Fatalf("partition entry %x, want active 0x0C at %d for %d sectors", e, startLBA, partSectors)
	}
	if !bytes.Equal(mbrSec[462:510], make([]byte, 48)) {
		t.Fatal("more than one partition entry")
	}

	bs := read(PartitionStart, sector)
	bps := int64(le16(bs[11:]))
	spc := int64(bs[13])
	reserved := int64(le16(bs[14:]))
	fats := int64(bs[16])
	totSec := int64(le32(bs[32:]))
	fatSz := int64(le32(bs[36:]))
	switch {
	case bs[0] != 0xeb && bs[0] != 0xe9:
		t.Fatalf("jump %x", bs[:3])
	case bps != sector:
		t.Fatalf("bytes per sector %d, want %d", bps, sector)
	case spc == 0 || spc&(spc-1) != 0 || spc*bps > 32*kib:
		t.Fatalf("sectors per cluster %d", spc)
	case reserved == 0 || fats != 2:
		t.Fatalf("reserved %d, FATs %d", reserved, fats)
	case le16(bs[17:]) != 0 || le16(bs[19:]) != 0 || le16(bs[22:]) != 0:
		t.Fatal("FAT12/16 fields set on a FAT32 volume")
	case totSec != int64(partSectors):
		t.Fatalf("total sectors %d, want %d", totSec, partSectors)
	case le32(bs[44:]) != 2 || le16(bs[48:]) != 1 || le16(bs[50:]) != 6:
		t.Fatal("root cluster, FSInfo or backup boot sector not where expected")
	case bs[66] != 0x29:
		t.Fatalf("extended boot signature %x", bs[66])
	case string(bs[71:82]) != fmt.Sprintf("%-11s", label):
		t.Fatalf("label %q", bs[71:82])
	case string(bs[82:90]) != "FAT32   ":
		t.Fatalf("fs type %q", bs[82:90])
	case bs[510] != 0x55 || bs[511] != 0xaa:
		t.Fatal("no boot sector signature")
	}
	if want := wantCluster(int64(partSectors) * sector); spc*bps != max(want, bps) {
		t.Fatalf("cluster %d bytes, want %d", spc*bps, max(want, bps))
	}
	clusters := (totSec - reserved - fats*fatSz) / spc
	if clusters < minClusters || clusters > 0x0ffffff5 {
		t.Fatalf("%d clusters is not FAT32", clusters)
	}
	if fatSz*bps < (clusters+2)*4 {
		t.Fatalf("FAT of %d sectors cannot map %d clusters", fatSz, clusters)
	}
	if !bytes.Equal(read(PartitionStart+6*sector, sector), bs) {
		t.Fatal("backup boot sector differs")
	}

	fsi := read(PartitionStart+sector, 512)
	if le32(fsi[0:]) != 0x41615252 || le32(fsi[484:]) != 0x61417272 || le32(fsi[508:]) != 0xaa550000 {
		t.Fatal("bad FSInfo signatures")
	}

	fat1 := read(PartitionStart+reserved*bps, fatSz*bps)
	fat2 := read(PartitionStart+(reserved+fatSz)*bps, fatSz*bps)
	if !bytes.Equal(fat1, fat2) {
		t.Fatal("FAT copies differ")
	}
	if le32(fat1[0:])&0x0fffffff != 0x0ffffff8 || le32(fat1[8:])&0x0fffffff < 0x0ffffff8 {
		t.Fatalf("FAT starts %x", fat1[:12])
	}
}

// wantCluster is Microsoft's FAT32 cluster size table as go-diskfs applies
// it, on the partition size.
func wantCluster(part int64) int64 {
	switch {
	case part <= 260*mib:
		return 512
	case part <= 8*gib:
		return 4 * kib
	case part <= 16*gib:
		return 8 * kib
	case part <= 32*gib:
		return 16 * kib
	}
	return 32 * kib
}
