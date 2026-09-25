//go:build linux || darwin

package fatfmt

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// fsck runs the host's FAT checker, read-only, on the partition. Neither
// fsck.fat nor fsck_msdos takes an offset, so the partition is first copied
// into an image of its own, holes and all.
func fsck(t *testing.T, disk *os.File, size, sector int64) {
	t.Helper()
	tool, args := fsckTool(sector)
	if tool == "" {
		return
	}
	part := extractPartition(t, disk, size, sector)
	out, err := exec.Command(tool, append(args, part)...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v\n%s", tool, err, out)
	}
}

func fsckTool(sector int64) (string, []string) {
	if runtime.GOOS == "darwin" {
		// fsck_msdos checks 512-byte sectors only.
		if p, err := exec.LookPath("fsck_msdos"); err == nil && sector == 512 {
			return p, []string{"-n"}
		}
		return "", nil
	}
	for _, name := range []string{"fsck.fat", "fsck.vfat"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, []string{"-n", "-v"}
		}
	}
	return "", nil
}

func extractPartition(t *testing.T, disk *os.File, size, sector int64) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "part.img")
	out, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	partSize := size - size%sector - PartitionStart
	if err := out.Truncate(partSize); err != nil {
		t.Fatal(err)
	}
	if err := copyData(disk, out, PartitionStart, partSize); err != nil {
		t.Fatal(err)
	}
	return path
}

// copyData copies the allocated extents of src[off:off+n] to dst[0:n].
func copyData(src, dst *os.File, off, n int64) error {
	fd := int(src.Fd())
	end := off + n
	for pos := off; pos < end; {
		data, err := unix.Seek(fd, pos, unix.SEEK_DATA)
		if errors.Is(err, unix.ENXIO) || data >= end {
			return nil
		}
		if err != nil {
			return fmt.Errorf("SEEK_DATA: %w", err)
		}
		hole, err := unix.Seek(fd, data, unix.SEEK_HOLE)
		if err != nil {
			return fmt.Errorf("SEEK_HOLE: %w", err)
		}
		hole = min(hole, end)
		if _, err := io.Copy(io.NewOffsetWriter(dst, data-off), io.NewSectionReader(src, data, hole-data)); err != nil {
			return err
		}
		pos = hole
	}
	return nil
}

// TestMtoolsRoundTrip writes a file tree of about 5 GiB, mostly holes, onto
// a fresh volume with mtools and reads it back. It needs mtools and several
// GiB of scratch space, so it runs when FLASHIT_MTOOLS=1 (Linux CI).
func TestMtoolsRoundTrip(t *testing.T) {
	if os.Getenv("FLASHIT_MTOOLS") != "1" {
		t.Skip("set FLASHIT_MTOOLS=1 to run")
	}
	for _, tool := range []string{"mcopy", "mmd", "mdir"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
	}
	const size = 16 << 30
	disk := formatImage(t, size, 512, "MTOOLS")
	part := extractPartition(t, disk, size, 512)

	src := t.TempDir()
	files := map[string]int64{
		// Not 4 GiB - 1: at 8 KiB clusters its chain is exactly 2^32 bytes,
		// which fsck.fat 4.2 wraps to 0 and reports as broken even on a
		// mkfs.fat volume.
		"sources/install.swm":  4<<30 - 1<<20,
		"sources/install2.swm": 1 << 30,
		"efi/boot/bootx64.efi": 1<<20 + 17,
		"setup.exe":            12345,
		"empty.txt":            0,
	}
	for name, n := range files {
		p := filepath.Join(src, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		// A marker at each end so a misplaced cluster shows up as a
		// different hash, not the same zeros.
		if n > 0 {
			f.WriteAt([]byte(name), 0)
			if n > int64(2*len(name)) {
				f.WriteAt([]byte(name), n-int64(len(name)))
			}
		}
		if err := f.Truncate(n); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}

	env := append(os.Environ(), "MTOOLS_SKIP_CHECK=1")
	run := func(name string, args ...string) {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
		}
	}
	run("mcopy", "-s", "-i", part, filepath.Join(src, "sources"), filepath.Join(src, "efi"),
		filepath.Join(src, "setup.exe"), filepath.Join(src, "empty.txt"), "::/")

	back := t.TempDir()
	run("mcopy", "-s", "-n", "-i", part, "::/sources", "::/efi", "::/setup.exe", "::/empty.txt", back)
	for name := range files {
		want := hashFile(t, filepath.Join(src, filepath.FromSlash(name)))
		got := hashFile(t, filepath.Join(back, filepath.FromSlash(name)))
		if !bytes.Equal(want, got) {
			t.Fatalf("%s came back different", name)
		}
	}
	if tool, args := fsckTool(512); tool != "" {
		if out, err := exec.Command(tool, append(args, part)...).CombinedOutput(); err != nil {
			t.Fatalf("%s after writing: %v\n%s", tool, err, out)
		}
	}
}

func hashFile(t *testing.T, path string) []byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatal(err)
	}
	return h.Sum(nil)
}

// The checker must fail a damaged volume, or the sweep proves nothing.
func TestFsckCatchesDamage(t *testing.T) {
	tool, args := fsckTool(512)
	if tool == "" {
		t.Skip("no FAT checker")
	}
	disk := formatImage(t, gib, 512, "DAMAGED")
	bs := make([]byte, 512)
	if _, err := disk.ReadAt(bs, PartitionStart); err != nil {
		t.Fatal(err)
	}
	fatSz := int64(binary.LittleEndian.Uint32(bs[36:]))
	// Cluster 2, the root directory, marked free in both FATs.
	for _, fat := range []int64{32, 32 + fatSz} {
		if _, err := disk.WriteAt(make([]byte, 4), PartitionStart+fat*512+8); err != nil {
			t.Fatal(err)
		}
	}
	part := extractPartition(t, disk, gib, 512)
	if out, err := exec.Command(tool, append(args, part)...).CombinedOutput(); err == nil {
		t.Fatalf("%s passed a volume with its root cluster free:\n%s", tool, out)
	}
}
