package steps

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kyleaupton/flashit/internal/core"
	"github.com/kyleaupton/flashit/internal/pipeline"
	"github.com/kyleaupton/flashit/internal/wim"
)

// SplitWim writes install.wim onto the volume as .swm parts that fit FAT32.
// It runs only when AnalyzeWim found the file too large; CopyFiles has
// already skipped it in that case.
type SplitWim struct{}

func (SplitWim) Key() string       { return "splitting-wim" }
func (SplitWim) Name() string      { return "Splitting install.wim" }
func (SplitWim) HasProgress() bool { return true }

func (SplitWim) Run(ctx context.Context, state *FlashContext, e core.Executor) error {
	if !state.NeedsSplit {
		e.Emit(core.Event{Type: core.EventLog, Message: "install.wim does not need splitting, skipping"})
		return nil
	}
	if core.DryRun {
		return pipeline.Simulate(ctx, e, 10*time.Second, 30)
	}

	e.Emit(core.Event{Type: core.EventLog, Message: "Splitting install.wim onto the drive..."})

	// SplitWithProgress appends .swm, 2.swm, ... to the prefix.
	splitPrefix := filepath.Join(state.USBMountPath, "sources", "install")
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
				Type:    core.EventProgress,
				Percent: percent,
				Message: fmt.Sprintf("Splitting: %.1f%% (part %d of %d)", percent, p.Part, p.TotalParts),
			})
		}
		return true
	}

	if err := wim.SplitWithProgress(ctx, state.InstallWimPath, splitPrefix, opts, progressCb); err != nil {
		return fmt.Errorf("failed to split WIM: %w", err)
	}

	e.Emit(core.Event{Type: core.EventLog, Message: "WIM split complete"})
	return nil
}

// Cleanup runs when this step or a later one fails; a half-written set of
// parts on the volume is removed either way.
func (SplitWim) Cleanup(ctx context.Context, state *FlashContext, e core.Executor) error {
	if !state.NeedsSplit || core.DryRun || state.USBMountPath == "" {
		return nil
	}
	e.Emit(core.Event{Type: core.EventLog, Message: "Removing split WIM parts..."})
	removeParts(filepath.Join(state.USBMountPath, "sources", "install"))
	return nil
}

func removeParts(prefix string) {
	parts, _ := filepath.Glob(prefix + "*.swm")
	for _, p := range parts {
		os.Remove(p)
	}
}
