package helper

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Microsoft/go-winio/vhd"
	"golang.org/x/sys/windows"

	"github.com/kyleaupton/flashit/internal/helper/validate"
	"github.com/kyleaupton/flashit/internal/proto"
)

const ioctlDiskSetDiskAttributes = 0x7c0f4

// TestVHD runs the disk ops against a VHDX it creates and attaches. It
// needs an elevated process (the windows-latest runner is one) and
// FLASHIT_VHD_TEST=1. The ops are called directly: the validated server must
// keep refusing a VHD, which is checked first.
func TestVHD(t *testing.T) {
	if os.Getenv("FLASHIT_VHD_TEST") != "1" {
		t.Skip("set FLASHIT_VHD_TEST=1 in an elevated shell to run")
	}
	path := attachVHD(t, 2)
	t.Logf("VHD attached as %s", path)
	d := &windowsDisk{log: slog.New(slog.NewTextHandler(os.Stderr, nil)), lockAttempts: 5, lockRetry: 200 * time.Millisecond}

	t.Run("gate refuses it", func(t *testing.T) {
		info, err := d.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Removable || info.Size == 0 {
			t.Fatalf("VHD reported as %+v", info)
		}
		sys, err := d.SystemDisks()
		if err != nil {
			t.Fatalf("system disks: %v", err)
		}
		t.Logf("system disks: %v", sys)
		for _, s := range sys {
			if s == path {
				t.Fatalf("the VHD counts as a system disk: %v", sys)
			}
		}
		var pe *proto.Error
		if err := validate.Target(info, sys); !errors.As(err, &pe) || pe.Code != proto.CodeNotRemovable {
			t.Fatalf("gate: %v", err)
		}
	})

	// Past the gate, let openForWrite's recheck accept the VHD's bus too.
	d.busOK = func(bus uint32) bool { return bus == validate.BusFileBackedVirtual || validate.RemovableBus(bus) }

	t.Run("write image", func(t *testing.T) {
		img := make([]byte, 8<<20+2048)
		if _, err := rand.Read(img); err != nil {
			t.Fatal(err)
		}
		// Enough of an MBR to look like a hybrid image to Windows.
		clear(img[446:510])
		img[446], img[450] = 0x80, 0x17
		binary.LittleEndian.PutUint32(img[454:], 64)
		binary.LittleEndian.PutUint32(img[458:], 8192)
		img[510], img[511] = 0x55, 0xaa

		raw, err := d.OpenRaw(path, NopGrant{})
		if err != nil {
			t.Fatal(err)
		}
		for off := 0; off < len(img); off += 1 << 20 {
			if _, err := raw.Write(img[off:min(off+1<<20, len(img))]); err != nil {
				raw.Close()
				t.Fatal(err)
			}
		}
		if err := raw.Sync(); err != nil {
			raw.Close()
			t.Fatal(err)
		}
		if err := raw.Close(); err != nil {
			t.Fatal(err)
		}
		got := readDisk(t, path, len(img))
		if sha256.Sum256(got) != sha256.Sum256(img) {
			t.Fatal("disk does not hold the image")
		}
	})

	var mp string
	t.Run("format", func(t *testing.T) {
		if _, err := d.Format(context.Background(), path, "fat32", "FLASHTEST"); err != nil {
			t.Fatal(err)
		}
		mp = mountpoint(t, path)
		out, err := exec.Command("chkdsk", chkdskTarget(mp)).CombinedOutput()
		t.Logf("chkdsk %s:\n%s", mp, out)
		if err != nil {
			t.Fatalf("chkdsk: %v", err)
		}
		if bytes.Contains(bytes.ToLower(out), []byte("found problems")) || bytes.Contains(bytes.ToLower(out), []byte("errors found")) {
			t.Fatal("chkdsk found problems")
		}
		if lbl := volumeLabel(t, mp); lbl != "FLASHTEST" {
			t.Fatalf("label %q", lbl)
		}
	})
	if mp == "" {
		t.Fatal("no volume to go on with")
	}

	t.Run("copy tree", func(t *testing.T) {
		files := map[string]int{
			`efi\boot\bootx64.efi`:  1<<20 + 7,
			`sources\install.swm`:   24 << 20,
			`sources\boot.wim`:      3<<20 + 512,
			`setup.exe`:             4096,
			`autorun.inf`:           0,
			`deep\a\b\c\d\file.txt`: 100,
		}
		sums := map[string][32]byte{}
		for name, n := range files {
			data := make([]byte, n)
			rand.Read(data)
			p := filepath.Join(mp, name)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, data, 0o644); err != nil {
				t.Fatal(err)
			}
			sums[name] = sha256.Sum256(data)
		}
		for name, want := range sums {
			data, err := os.ReadFile(filepath.Join(mp, name))
			if err != nil {
				t.Fatal(err)
			}
			if sha256.Sum256(data) != want {
				t.Fatalf("%s came back different", name)
			}
		}
		out, err := exec.Command("chkdsk", chkdskTarget(mp)).CombinedOutput()
		if err != nil {
			t.Fatalf("chkdsk after copying: %v\n%s", err, out)
		}
	})

	t.Run("busy", func(t *testing.T) {
		held, err := os.Open(filepath.Join(mp, "setup.exe"))
		if err != nil {
			t.Fatal(err)
		}
		err = d.Unmount(path)
		held.Close()
		if !errors.Is(err, errVolumeBusy) {
			t.Fatalf("unmount with a file open: %v, want busy", err)
		}
		if _, err := d.OpenRaw(path, NopGrant{}); err == nil {
			t.Fatal("opened for writing while busy")
		}
		if err := d.Unmount(path); err != nil {
			t.Fatalf("unmount once released: %v", err)
		}
	})

	t.Run("eject", func(t *testing.T) {
		// A VHD has no media to eject; only the unmount must work.
		if err := d.Eject(path); err != nil {
			t.Logf("eject: %v", err)
		}
	})
}

// attachVHD creates a dynamic VHDX of gib GiB and attaches it until the
// test ends, returning \\.\PhysicalDriveN.
func attachVHD(t *testing.T, gib uint32) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "flashit-test.vhdx")
	if err := vhd.CreateVhdx(file, gib, 1); err != nil {
		t.Fatal(err)
	}
	h, err := vhd.OpenVirtualDisk(file, vhd.VirtualDiskAccessNone, vhd.OpenVirtualDiskFlagNone)
	if err != nil {
		t.Fatal(err)
	}
	if err := vhd.AttachVirtualDisk(h, vhd.AttachVirtualDiskFlagNone, &vhd.AttachVirtualDiskParameters{Version: 2}); err != nil {
		windows.CloseHandle(windows.Handle(h))
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := vhd.DetachVirtualDisk(h); err != nil {
			t.Logf("detach: %v", err)
		}
		windows.CloseHandle(windows.Handle(h))
	})
	path, err := vhd.GetVirtualDiskPhysicalPath(h)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := validate.PhysicalDrivePath(path); err != nil {
		t.Fatalf("VHD path %q: %v", path, err)
	}
	setOnline(t, path)
	return path
}

// setOnline clears the offline and read-only attributes a SAN policy may put
// on a new disk.
func setOnline(t *testing.T, path string) {
	h, err := openDevice(path, windows.GENERIC_READ|windows.GENERIC_WRITE, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	in := make([]byte, 40)
	binary.LittleEndian.PutUint32(in[0:], 40)
	binary.LittleEndian.PutUint64(in[16:], 0x3) // mask: offline, read-only
	if _, err := ioctl(h, ioctlDiskSetDiskAttributes, in, nil); err != nil {
		t.Logf("IOCTL_DISK_SET_DISK_ATTRIBUTES: %v", err)
	}
}

func readDisk(t *testing.T, path string, n int) []byte {
	t.Helper()
	h, err := openDevice(path, windows.GENERIC_READ, windows.FILE_FLAG_NO_BUFFERING)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	buf := make([]byte, (n+4095)&^4095)
	if _, err := (&diskIO{h: h}).ReadAt(buf, 0); err != nil {
		t.Fatal(err)
	}
	return buf[:n]
}

// mountpoint waits for Windows to mount the disk's new volume and returns
// its letter, or mounts it on a folder if automount gave it none.
func mountpoint(t *testing.T, path string) string {
	t.Helper()
	_, n, _ := validate.PhysicalDrivePath(path)
	deadline := time.Now().Add(30 * time.Second)
	for {
		vols, err := volumes()
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range vols {
			disks, err := volumeDisks(v)
			if err != nil || !onDisk(disks, n) {
				continue
			}
			names, _ := volumePathNames(v)
			if len(names) > 0 {
				return names[0]
			}
			if time.Now().After(deadline.Add(-20 * time.Second)) {
				dir := t.TempDir() + `\`
				if err := windows.SetVolumeMountPoint(windows.StringToUTF16Ptr(dir), windows.StringToUTF16Ptr(v+`\`)); err != nil {
					t.Fatalf("mount %s on %s: %v", v, dir, err)
				}
				t.Cleanup(func() { windows.DeleteVolumeMountPoint(windows.StringToUTF16Ptr(dir)) })
				return dir
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("no volume appeared on the disk")
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// chkdskTarget is E: for a drive letter and the folder itself otherwise.
// Without /f chkdsk only reads.
func chkdskTarget(mp string) string {
	if len(mp) == 3 && strings.HasSuffix(mp, `:\`) {
		return mp[:2]
	}
	return mp
}

func volumeLabel(t *testing.T, root string) string {
	t.Helper()
	name := make([]uint16, windows.MAX_PATH+1)
	fs := make([]uint16, windows.MAX_PATH+1)
	if err := windows.GetVolumeInformation(windows.StringToUTF16Ptr(root), &name[0], uint32(len(name)), nil, nil, nil, &fs[0], uint32(len(fs))); err != nil {
		t.Fatal(err)
	}
	if got := windows.UTF16ToString(fs); got != "FAT32" {
		t.Fatalf("filesystem %q", got)
	}
	return windows.UTF16ToString(name)
}
