// isotest compares what internal/isofs reads from each ISO with what the
// host's own mount shows, path by path, size and SHA-256. Run it on every
// Windows ISO at hand before bumping golift.io/udf.
//
//	isotest <iso>...
//
// On macOS it mounts with hdiutil; on Linux it loop-mounts and needs root.
// An image with no UDF file system, such as a Linux hybrid ISO, is skipped.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/kyleaupton/flashit/internal/isofs"
	"github.com/kyleaupton/flashit/internal/isofs/isocompare"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: isotest <iso>...")
		os.Exit(2)
	}
	failed := false
	for _, path := range os.Args[1:] {
		if err := check(path); err != nil {
			fmt.Printf("FAIL %s: %v\n", path, err)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}

func check(path string) error {
	start := time.Now()
	im, err := isofs.Open(path)
	if errors.Is(err, isofs.ErrNoUDF) {
		fmt.Printf("SKIP %s: no UDF file system\n", path)
		return nil
	}
	if err != nil {
		return err
	}
	defer im.Close()
	opened := time.Since(start)

	dir, unmount, err := isocompare.Mount(context.Background(), path)
	if err != nil {
		return err
	}
	defer unmount()

	sum, diffs, err := isocompare.Trees(im, os.DirFS(dir))
	if err != nil {
		return err
	}
	for _, d := range diffs {
		fmt.Println("  " + d)
	}
	if len(diffs) > 0 {
		return fmt.Errorf("%d differences from the mount at %s", len(diffs), dir)
	}
	fmt.Printf("OK   %s: %d files, %d directories, %d bytes match the mount (index %v, total %v)\n",
		path, sum.Files, sum.Dirs, sum.Bytes, opened.Round(time.Millisecond), time.Since(start).Round(time.Second))
	return nil
}
