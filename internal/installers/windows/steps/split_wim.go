package steps

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"github.com/kyleaupton/flashit/internal/core"
	"github.com/kyleaupton/flashit/internal/pipeline"
	"github.com/kyleaupton/flashit/internal/wim"
)

// SplitWim splits install.wim into smaller chunks for FAT32 compatibility.
// This step is only executed if NeedsSplit is true.
type SplitWim struct{}

func (SplitWim) Key() string       { return "splitting-wim" }
func (SplitWim) Name() string      { return "Splitting install.wim" }
func (SplitWim) HasProgress() bool { return true }

func (SplitWim) Run(ctx context.Context, state *FlashContext, e core.Executor) error {
	if core.DryRun {
		if !state.NeedsSplit {
			e.Emit(core.Event{Type: "log", Message: "install.wim does not need splitting, skipping"})
			return nil
		}
		return pipeline.Simulate(ctx, e, 10*time.Second, 30)
	}

	if !state.NeedsSplit {
		e.Emit(core.Event{Type: "log", Message: "install.wim does not need splitting, skipping"})
		return nil
	}

	e.Emit(core.Event{Type: "log", Message: "Splitting install.wim for FAT32 compatibility..."})

	// Create temp directory for split files
	tempDir := filepath.Join(os.TempDir(), "flashit-wim-"+uuid.New().String())
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}

	state.SWMTempDir = tempDir

	// Split the WIM file (prefix only - SplitWithProgress appends .swm, 2.swm, etc.)
	splitPrefix := filepath.Join(tempDir, "install")
	opts := wim.SplitOptions{
		PartSizeMiB: 3800, // 3800 MiB parts for safety margin under FAT32's 4GB limit
	}

	progressCb := func(p wim.Progress) bool {
		select {
		case <-ctx.Done():
			return false
		default:
		}

		if p.TotalBytes > 0 {
			percent := float64(p.DoneBytes) * 100.0 / float64(p.TotalBytes)
			e.Emit(core.Event{
				Type:    "progress",
				Percent: percent,
				Message: fmt.Sprintf("Splitting: %.1f%% (part %d)", percent, p.Part),
			})
		}
		return true
	}

	if err := wim.SplitWithProgress(ctx, state.InstallWimPath, splitPrefix, opts, progressCb); err != nil {
		return fmt.Errorf("failed to split WIM: %w", err)
	}

	e.Emit(core.Event{Type: "log", Message: "WIM split complete"})
	return nil
}

// Cleanup removes the temp directory with split WIM files.
func (SplitWim) Cleanup(ctx context.Context, state *FlashContext, e core.Executor) error {
	if state.SWMTempDir != "" {
		e.Emit(core.Event{Type: "log", Message: "Cleaning up split WIM files..."})
		os.RemoveAll(state.SWMTempDir)
		state.SWMTempDir = ""
	}
	return nil
}
