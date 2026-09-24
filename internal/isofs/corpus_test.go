package isofs_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kyleaupton/flashit/internal/isofs"
	"github.com/kyleaupton/flashit/internal/isofs/isocompare"
)

// TestCorpus compares isofs with the host mount for every .iso in
// FLASHIT_ISO_CORPUS, as cmd/isotest does. It needs no prompt on macOS; on
// Linux it needs root.
func TestCorpus(t *testing.T) {
	dir := os.Getenv("FLASHIT_ISO_CORPUS")
	if dir == "" {
		t.Skip("set FLASHIT_ISO_CORPUS to a directory of ISOs")
	}
	isos, _ := filepath.Glob(filepath.Join(dir, "*.iso"))
	if len(isos) == 0 {
		t.Fatalf("no .iso files in %s", dir)
	}
	for _, path := range isos {
		t.Run(filepath.Base(path), func(t *testing.T) {
			im, err := isofs.Open(path)
			if errors.Is(err, isofs.ErrNoUDF) {
				t.Skip("no UDF file system")
			}
			if err != nil {
				t.Fatal(err)
			}
			defer im.Close()
			mnt, unmount, err := isocompare.Mount(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			defer unmount()
			sum, diffs, err := isocompare.Trees(im, os.DirFS(mnt))
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range diffs {
				t.Error(d)
			}
			t.Logf("%d files, %d directories, %d bytes", sum.Files, sum.Dirs, sum.Bytes)
		})
	}
}
