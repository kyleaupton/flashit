package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kyleaupton/flashit/internal/wim"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: wimtest <path-to-wim>")
		fmt.Println("Example: wimtest /Volumes/CCCOMA_X64FRE_EN-US_DV9/sources/install.wim")
		os.Exit(1)
	}

	wimPath := os.Args[1]

	// First test: parse the WIM
	fmt.Println("=== Testing WIM Parser ===")
	f, err := os.Open(wimPath)
	if err != nil {
		fmt.Printf("Failed to open WIM: %v\n", err)
		os.Exit(1)
	}

	r, err := wim.NewReader(f)
	if err != nil {
		f.Close()
		fmt.Printf("Failed to parse WIM: %v\n", err)
		os.Exit(1)
	}

	hdr := r.Header()
	fmt.Printf("WIM File: %s\n", wimPath)
	fmt.Printf("  Images: %d\n", len(r.Image))
	fmt.Printf("  Blobs:  %d\n", len(r.Streams()))
	fmt.Printf("  Part:   %d of %d\n", hdr.PartNumber, hdr.TotalParts)
	f.Close()

	// Second test: split the WIM
	fmt.Println("\n=== Testing WIM Splitter ===")

	// Create temp directory for output
	tmpDir, err := os.MkdirTemp("", "wimtest-*")
	if err != nil {
		fmt.Printf("Failed to create temp dir: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	dstPrefix := filepath.Join(tmpDir, "install")
	fmt.Printf("Output prefix: %s\n", dstPrefix)

	ctx := context.Background()
	start := time.Now()
	lastReport := time.Now()

	opts := wim.SplitOptions{
		PartSizeMiB: 3800, // ~3.7GB parts
	}

	err = wim.SplitFileWithProgress(ctx, wimPath, dstPrefix, opts, func(p wim.Progress) bool {
		now := time.Now()
		// Report progress every 500ms or on phase change
		if now.Sub(lastReport) > 500*time.Millisecond || p.Phase == "analyzing" {
			lastReport = now
			switch p.Phase {
			case "analyzing":
				fmt.Printf("Phase: analyzing WIM structure...\n")
			case "splitting":
				if p.TotalBytes > 0 {
					pct := float64(p.DoneBytes) / float64(p.TotalBytes) * 100
					fmt.Printf("Phase: splitting - Part %d/%d - %.1f%% (%.2f GB / %.2f GB)\n",
						p.Part, p.TotalParts, pct,
						float64(p.DoneBytes)/(1024*1024*1024),
						float64(p.TotalBytes)/(1024*1024*1024))
				}
			}
		}
		return true
	})

	elapsed := time.Since(start)

	if err != nil {
		fmt.Printf("\nSplit FAILED: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\nSplit completed in %v\n", elapsed)

	// List output files
	fmt.Println("\n=== Output Files ===")
	entries, _ := os.ReadDir(tmpDir)
	var totalSize int64
	for _, e := range entries {
		info, _ := e.Info()
		size := info.Size()
		totalSize += size
		fmt.Printf("  %s: %.2f GB\n", e.Name(), float64(size)/(1024*1024*1024))
	}
	fmt.Printf("  Total: %.2f GB\n", float64(totalSize)/(1024*1024*1024))

	// Verify output files can be parsed
	fmt.Println("\n=== Verifying Output Files ===")
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".swm" {
			continue
		}
		swmPath := filepath.Join(tmpDir, e.Name())
		sf, err := os.Open(swmPath)
		if err != nil {
			fmt.Printf("  %s: FAILED to open: %v\n", e.Name(), err)
			continue
		}
		sr, err := wim.NewReader(sf)
		if err != nil {
			fmt.Printf("  %s: FAILED to parse: %v\n", e.Name(), err)
			sf.Close()
			continue
		}
		shdr := sr.Header()
		fmt.Printf("  %s: OK (part %d/%d, %d streams)\n",
			e.Name(), shdr.PartNumber, shdr.TotalParts, len(sr.Streams()))
		sf.Close()
	}

	fmt.Println("\nTest completed successfully!")
}
