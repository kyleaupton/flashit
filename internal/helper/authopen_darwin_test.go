//go:build darwin

package helper

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/kyleaupton/flashit/internal/proto"
)

// The test binary doubles as a stand-in for /usr/libexec/authopen: with
// FLASHIT_TEST_AUTHOPEN set it speaks authopen's side of the protocol and
// exits, so authopen() can be exercised against a temp file.
func TestMain(m *testing.M) {
	if mode := os.Getenv("FLASHIT_TEST_AUTHOPEN"); mode != "" {
		os.Exit(authopenStub(mode))
	}
	os.Exit(m.Run())
}

func authopenStub(mode string) int {
	args := os.Args[1:]
	if len(args) != 5 || args[0] != "-stdoutpipe" || args[1] != "-extauth" || args[2] != "-o" {
		fmt.Fprintf(os.Stderr, "stub: unexpected argv %q\n", args)
		return 64
	}
	path := args[4]
	form := make([]byte, externalFormLength)
	if _, err := os.Stdin.Read(form); err != nil {
		fmt.Fprintf(os.Stderr, "stub: read form: %v\n", err)
		return 64
	}
	if want := os.Getenv("FLASHIT_TEST_FORM"); hex.EncodeToString(form) != want {
		fmt.Fprintf(os.Stderr, "stub: form %x, want %s\n", form, want)
		return 64
	}
	out, err := net.FileConn(os.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "stub: stdout is not a socket: %v\n", err)
		return 64
	}
	uc := out.(*net.UnixConn)
	switch mode {
	case "ok":
		f, err := os.OpenFile(path, os.O_RDWR, 0)
		if err != nil {
			fmt.Fprintf(os.Stderr, "authopen: couldn't open %s: %v\n", path, err)
			return 1
		}
		_, _, err = uc.WriteMsgUnix([]byte{0, 0}, syscall.UnixRights(int(f.Fd())), nil)
		if err != nil {
			return 64
		}
		return 0
	case "eperm":
		fmt.Fprintf(os.Stderr, "authopen: couldn't open %s: Operation not permitted\n", path)
		uc.Write([]byte{0, 1})
		return 1
	case "cancelled":
		fmt.Fprintln(os.Stderr, "AuthorizationCopyRights failed: The authorization was canceled by the user.")
		uc.Write([]byte{0, 0x59})
		return 1
	}
	return 64
}

func stubForm(t *testing.T) []byte {
	t.Helper()
	form := bytes.Repeat([]byte{0xa5}, externalFormLength)
	t.Setenv("FLASHIT_TEST_FORM", hex.EncodeToString(form))
	return form
}

func TestAuthopenReceivesDescriptor(t *testing.T) {
	t.Setenv("FLASHIT_TEST_AUTHOPEN", "ok")
	form := stubForm(t)
	path := filepath.Join(t.TempDir(), "disk")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := authopen(os.Args[0], path, unix.O_RDWR, form)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write([]byte("new")); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "new" {
		t.Fatalf("wrote through the received descriptor, file holds %q", got)
	}
}

func TestAuthopenFailures(t *testing.T) {
	form := stubForm(t)
	path := filepath.Join(t.TempDir(), "disk")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("FLASHIT_TEST_AUTHOPEN", "eperm")
	_, err := authopen(os.Args[0], path, unix.O_RDWR, form)
	var pe *proto.Error
	if !errors.As(err, &pe) || pe.Code != proto.CodeTCCDenied {
		t.Fatalf("EPERM after the sheet must be tcc_denied, got %v", err)
	}

	t.Setenv("FLASHIT_TEST_AUTHOPEN", "cancelled")
	_, err = authopen(os.Args[0], path, unix.O_RDWR, form)
	if err == nil || errors.As(err, &pe) || !strings.Contains(err.Error(), "canceled by the user") {
		t.Fatalf("other exits are plain errors carrying stderr, got %v", err)
	}

	// A form the stub does not expect is refused with a nonzero exit.
	t.Setenv("FLASHIT_TEST_AUTHOPEN", "ok")
	if _, err := authopen(os.Args[0], path, unix.O_RDWR, bytes.Repeat([]byte{1}, externalFormLength)); err == nil {
		t.Fatal("expected an error for a form the stub rejects")
	}

	if _, err := authopen(filepath.Join(t.TempDir(), "missing"), path, unix.O_RDWR, form); err == nil {
		t.Fatal("expected an error for a missing binary")
	}
}

func TestOpenRawNeedsMatchingGrant(t *testing.T) {
	d := &darwinDisk{authopen: os.Args[0]}
	for _, g := range []Grant{nil, NopGrant{}, &authzGrant{raw: "/dev/rdisk9", info: DeviceInfo{Path: "/dev/disk9"}}} {
		if _, err := d.OpenRaw("/dev/disk8", g); err == nil {
			t.Fatalf("OpenRaw accepted grant %#v for another device", g)
		}
	}
}

// usbGrant is a grant for the fixtures' USB stick disk4, pointed at a raw
// path the stub can open: a character device the test names.
func usbGrant(t *testing.T, raw string) *authzGrant {
	t.Helper()
	fd, du := thisMac(t)
	info, err := deviceInfoFrom("/dev/disk4", mustInfo(t, du, "/dev/disk4"))
	if err != nil {
		t.Fatal(err)
	}
	_ = fd
	form := stubForm(t)
	g := &authzGrant{raw: raw, info: info}
	copy(g.form[:], form)
	return g
}

func mustInfo(t *testing.T, du diskutil, dev string) duInfo {
	t.Helper()
	inf, err := du.info(context.Background(), dev)
	if err != nil {
		t.Fatal(err)
	}
	return inf
}

// OpenRaw with the DA claim stubbed out: the stub authopen opens /dev/null,
// which is a character device, so the descriptor check passes when it is
// asked for /dev/null and fails when the stub hands back a regular file.
func TestOpenRawChecksDeviceAndDescriptor(t *testing.T) {
	t.Setenv("FLASHIT_TEST_AUTHOPEN", "ok")
	newDisk := func(t *testing.T, infos map[string]string) (*darwinDisk, *int) {
		t.Helper()
		releases := 0
		fd, du := thisMac(t)
		for k, v := range infos {
			fd.infos[k] = v
		}
		d := &darwinDisk{du: du, log: slog.New(slog.DiscardHandler), authopen: os.Args[0],
			claim: func(string) (func(), error) { return func() { releases++ }, nil }}
		return d, &releases
	}

	t.Run("same disk, real character device", func(t *testing.T) {
		d, releases := newDisk(t, nil)
		g := usbGrant(t, "/dev/null")
		raw, err := d.OpenRaw("/dev/disk4", g)
		if err != nil {
			t.Fatal(err)
		}
		if *releases != 0 {
			t.Fatal("claim released while the device is open")
		}
		raw.Close()
		if *releases != 1 {
			t.Fatal("claim not released on close")
		}
	})

	t.Run("disk replaced since validation", func(t *testing.T) {
		// disk4 now answers with the disk image's plist: same node, other disk.
		d, releases := newDisk(t, map[string]string{"disk4": "info-disk-image.plist"})
		g := usbGrant(t, "/dev/null")
		_, err := d.OpenRaw("/dev/disk4", g)
		var pe *proto.Error
		if !errors.As(err, &pe) || pe.Code != proto.CodeInvalidDevice {
			t.Fatalf("got %v, want invalid_device", err)
		}
		if *releases != 1 {
			t.Fatal("claim not released after the recheck refused")
		}
	})

	t.Run("disk gone since validation", func(t *testing.T) {
		d, _ := newDisk(t, nil)
		g := usbGrant(t, "/dev/null")
		g.info.Path, g.info.WholeDisk = "/dev/disk7", "/dev/disk7"
		if _, err := d.OpenRaw("/dev/disk7", g); err == nil {
			t.Fatal("opened a disk diskutil no longer knows")
		}
	})

	t.Run("descriptor is not the device", func(t *testing.T) {
		d, releases := newDisk(t, nil)
		file := filepath.Join(t.TempDir(), "not-a-disk")
		if err := os.WriteFile(file, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		g := usbGrant(t, file)
		if _, err := d.OpenRaw("/dev/disk4", g); err == nil || !strings.Contains(err.Error(), "not "+file) {
			t.Fatalf("accepted a regular file from authopen: %v", err)
		}
		if *releases != 1 {
			t.Fatal("claim not released after the descriptor was refused")
		}
	})
}

func TestVerifyRawDescriptor(t *testing.T) {
	null, err := os.Open("/dev/null")
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	if err := verifyRawDescriptor(null, "/dev/null"); err != nil {
		t.Fatal(err)
	}
	if err := verifyRawDescriptor(null, "/dev/zero"); err == nil {
		t.Fatal("accepted a character device with another rdev")
	}
	regular, err := os.CreateTemp(t.TempDir(), "x")
	if err != nil {
		t.Fatal(err)
	}
	defer regular.Close()
	if err := verifyRawDescriptor(regular, "/dev/null"); err == nil {
		t.Fatal("accepted a regular file")
	}
}

func TestTCCProbeResult(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want proto.ErrorCode
	}{
		{nil, ""},
		{unix.EACCES, ""},
		{unix.EPERM, proto.CodeTCCDenied},
		{unix.ENOENT, proto.CodeInternal},
	} {
		err := tccProbeResult("/dev/rdisk9", tc.err)
		if tc.want == "" {
			if err != nil {
				t.Fatalf("%v: got %v", tc.err, err)
			}
			continue
		}
		var pe *proto.Error
		if !errors.As(err, &pe) || pe.Code != tc.want {
			t.Fatalf("%v: got %v, want %s", tc.err, err, tc.want)
		}
	}
}

func TestRawDevicePath(t *testing.T) {
	if got := rawDevicePath(DeviceInfo{Path: "/dev/disk12", WholeDisk: "/dev/disk12"}); got != "/dev/rdisk12" {
		t.Fatalf("got %s", got)
	}
}

// Authorize must not reach authd for a token that is not an external form,
// and must refuse before the sheet when the probe reports a TCC denial.
func TestAuthorizeRefusesBeforeTheSheet(t *testing.T) {
	dev := DeviceInfo{Path: "/dev/disk9", WholeDisk: "/dev/disk9"}
	var probed []string
	a := authz{probe: func(raw string) error {
		probed = append(probed, raw)
		return proto.NewError(proto.CodeTCCDenied, "denied")
	}}
	for _, token := range []string{"", "not base64!", "c2hvcnQ="} {
		if _, err := a.Authorize(proto.OpWriteImage, token, dev); err == nil {
			t.Fatalf("token %q was accepted", token)
		}
	}
	if len(probed) != 0 {
		t.Fatal("probed the device for a malformed token")
	}
	good := base64.StdEncoding.EncodeToString(make([]byte, externalFormLength))
	_, err := a.Authorize(proto.OpWriteImage, good, dev)
	var pe *proto.Error
	if !errors.As(err, &pe) || pe.Code != proto.CodeTCCDenied {
		t.Fatalf("got %v, want tcc_denied", err)
	}
	if len(probed) != 1 || probed[0] != "/dev/rdisk9" {
		t.Fatalf("probed %v", probed)
	}
}
