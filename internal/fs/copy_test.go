package fs

import (
	"context"
	"errors"
	iofs "io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func testTree() fstest.MapFS {
	return fstest.MapFS{
		"README.TXT":              {Data: []byte("hello")},
		"sources/install.wim":     {Data: []byte(strings.Repeat("w", 5000))},
		"sources/boot.wim":        {Data: []byte("boot")},
		"sources/en-us/empty.dat": {Data: nil},
		"efi/boot":                {Mode: iofs.ModeDir},
	}
}

func TestCopyDir(t *testing.T) {
	dst := t.TempDir()
	var last CopyProgress
	opts := CopyDirOptions{
		Filter:     func(p string, info iofs.FileInfo) bool { return p != "sources/install.wim" },
		BufferSize: 3,
	}
	err := CopyDir(context.Background(), testTree(), dst, opts, func(p CopyProgress) bool {
		last = p
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"README.TXT": "hello", "sources/boot.wim": "boot", "sources/en-us/empty.dat": ""} {
		got, err := os.ReadFile(filepath.Join(dst, filepath.FromSlash(name)))
		if err != nil || string(got) != want {
			t.Errorf("%s: %q, %v", name, got, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dst, "sources", "install.wim")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("filtered file was copied: %v", err)
	}
	if st, err := os.Stat(filepath.Join(dst, "efi", "boot")); err != nil || !st.IsDir() {
		t.Errorf("empty directory not created: %v", err)
	}
	if last.Written != 9 || last.Total != 9 {
		t.Errorf("last progress %+v, want 9 of 9", last)
	}
}

func TestCopyDir_RefusesSymlink(t *testing.T) {
	src := fstest.MapFS{"link": {Data: []byte("/etc/passwd"), Mode: iofs.ModeSymlink}}
	err := CopyDir(context.Background(), src, t.TempDir(), CopyDirOptions{}, nil)
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("got %v", err)
	}
}

func TestCopyDir_RefusesOverwrite(t *testing.T) {
	dst := t.TempDir()
	if err := os.WriteFile(filepath.Join(dst, "README.TXT"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CopyDir(context.Background(), testTree(), dst, CopyDirOptions{}, nil); !errors.Is(err, os.ErrExist) {
		t.Fatalf("got %v", err)
	}
}

func TestCopyDir_Cancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := CopyDir(ctx, testTree(), t.TempDir(), CopyDirOptions{}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	err := CopyDir(context.Background(), testTree(), t.TempDir(), CopyDirOptions{}, func(CopyProgress) bool { return false })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("progress refusal: %v", err)
	}
}

func TestDestination(t *testing.T) {
	dst := t.TempDir()
	if got, err := destination(dst, "sources/boot.wim"); err != nil || got != filepath.Join(dst, "sources", "boot.wim") {
		t.Fatalf("got %q, %v", got, err)
	}
	for _, bad := range []string{"..", "../x", "a/../../x", "/etc/passwd", "a//b", ""} {
		if got, err := destination(dst, bad); err == nil {
			t.Errorf("%q accepted as %q", bad, got)
		}
	}
}
