package steps

import (
	"context"
	"fmt"
	iofs "io/fs"
	"os"
	"time"

	"github.com/kyleaupton/flashit/internal/core"
	"github.com/kyleaupton/flashit/internal/fs"
	"github.com/kyleaupton/flashit/internal/pipeline"
)

// CopyFiles copies all files from the ISO to USB, skipping files > FAT32 limit.
type CopyFiles struct{}

func (CopyFiles) Key() string       { return "copying-files" }
func (CopyFiles) Name() string      { return "Copying files" }
func (CopyFiles) HasProgress() bool { return true }

func (CopyFiles) Run(ctx context.Context, state *FlashContext, e core.Executor) error {
	if core.DryRun {
		var size uint64
		if st, err := os.Stat(state.ISOPath); err == nil {
			size = uint64(st.Size())
		}
		return pipeline.SimulateTransfer(ctx, e, 15*time.Second, 60, size)
	}

	e.Emit(core.Event{Type: "log", Message: "Copying files..."})

	opts := fs.CopyDirOptions{
		Filter: func(relPath string, info iofs.FileInfo) bool {
			if info.Size() > fat32MaxFileSize {
				e.Emit(core.Event{Type: "log", Message: fmt.Sprintf("Skipping large file %s (%.2f GB)", relPath, float64(info.Size())/1e9)})
				return false
			}
			return true
		},
		SyncAfter:        true,
		ProgressInterval: 250 * time.Millisecond,
	}

	err := fs.CopyDir(ctx, state.Source, state.USBMountPath, opts, func(p fs.CopyProgress) bool {
		if p.Total > 0 {
			percent := float64(p.Written) * 100.0 / float64(p.Total)
			e.Emit(core.Event{
				Type:    "progress",
				Percent: percent,
				Message: fmt.Sprintf("Copying: %.1f / %.1f GB", float64(p.Written)/1e9, float64(p.Total)/1e9),
				Bytes:   uint64(p.Written),
				Total:   uint64(p.Total),
			})
		}
		return true
	})

	if err != nil {
		return fmt.Errorf("failed to copy files: %w", err)
	}

	e.Emit(core.Event{Type: "log", Message: "Files copied successfully"})
	return nil
}
