package wim

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/kyleaupton/flashit/internal/isofs"
	"github.com/kyleaupton/flashit/internal/isofs/isocompare"
)

// TestSplitFromISOMatchesMount splits install.wim once as isofs reads it
// and once from the host mount, and requires identical parts. Set
// FLASHIT_WIN_ISO to a Windows ISO; it needs the WIM's size in free temp
// space.
func TestSplitFromISOMatchesMount(t *testing.T) {
	isoPath := os.Getenv("FLASHIT_WIN_ISO")
	if isoPath == "" {
		t.Skip("set FLASHIT_WIN_ISO to a Windows ISO")
	}
	defer func(f func() (guid, error)) { newSplitGUID = f }(newSplitGUID)
	newSplitGUID = func() (guid, error) { return guid{Data1: 1, Data2: 2, Data3: 3}, nil }

	im, err := isofs.Open(isoPath)
	if err != nil {
		t.Fatal(err)
	}
	defer im.Close()
	f, err := im.Open("sources/install.wim")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	st, _ := f.Stat()

	mnt, unmount, err := isocompare.Mount(context.Background(), isoPath)
	if err != nil {
		t.Fatal(err)
	}
	defer unmount()

	opts := SplitOptions{PartSizeMiB: 3800}
	fromISO := splitAndHash(t, func(prefix string) error {
		return SplitWithProgress(context.Background(), f.(io.ReaderAt), st.Size(), prefix, opts, nil)
	})
	fromMount := splitAndHash(t, func(prefix string) error {
		return SplitFileWithProgress(context.Background(), filepath.Join(mnt, "sources", "install.wim"), prefix, opts, nil)
	})
	if len(fromMount) < 2 || len(fromISO) != len(fromMount) {
		t.Fatalf("isofs split wrote %d parts, mount split %d", len(fromISO), len(fromMount))
	}
	for name, sum := range fromMount {
		if !bytes.Equal(fromISO[name], sum) {
			t.Errorf("%s differs", name)
		}
	}
	t.Logf("%d parts identical", len(fromMount))
}

// splitAndHash runs one split into a fresh directory, hashes the parts and
// removes them, so only one copy of the parts is on disk at a time.
func splitAndHash(t *testing.T, split func(prefix string) error) map[string][]byte {
	t.Helper()
	dir, err := os.MkdirTemp("", "wimsplit-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	if err := split(filepath.Join(dir, "install")); err != nil {
		t.Fatal(err)
	}
	parts, _ := filepath.Glob(filepath.Join(dir, "*.swm"))
	sums := map[string][]byte{}
	for _, p := range parts {
		sums[filepath.Base(p)] = hashFile(t, p)
	}
	return sums
}

func hashFile(t *testing.T, p string) []byte {
	f, err := os.Open(p)
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
