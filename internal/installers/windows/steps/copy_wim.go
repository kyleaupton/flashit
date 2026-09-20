package steps

import (
	"context"
	"fmt"
	"time"

	"github.com/kyleaupton/flashit/internal/core"
	"github.com/kyleaupton/flashit/internal/pipeline"
	"github.com/kyleaupton/flashit/internal/wim"
)

// CopyWim copies the split WIM files to the USB.
// This step is only executed if NeedsSplit is true.
type CopyWim struct{}

func (CopyWim) Key() string       { return "copying-wim" }
func (CopyWim) Name() string      { return "Copying split WIM files" }
func (CopyWim) HasProgress() bool { return true }

func (CopyWim) Run(ctx context.Context, state *FlashContext, e core.Executor) error {
	if core.DryRun {
		if !state.NeedsSplit {
			e.Emit(core.Event{Type: "log", Message: "No split WIM to copy, skipping"})
			return nil
		}
		return pipeline.Simulate(ctx, e, 5*time.Second, 15)
	}

	if !state.NeedsSplit {
		e.Emit(core.Event{Type: "log", Message: "No split WIM to copy, skipping"})
		return nil
	}

	e.Emit(core.Event{Type: "log", Message: "Copying split WIM files to USB..."})

	copyCb := func(done, total int64) bool {
		select {
		case <-ctx.Done():
			return false
		default:
		}

		if total > 0 {
			percent := float64(done) * 100.0 / float64(total)
			e.Emit(core.Event{
				Type:    "progress",
				Percent: percent,
				Message: fmt.Sprintf("Copying: %.1f / %.1f GB", float64(done)/1e9, float64(total)/1e9),
			})
		}
		return true
	}

	if err := wim.CopySWMs(ctx, state.SWMTempDir, state.USBMountPath, copyCb); err != nil {
		return fmt.Errorf("failed to copy split WIM files: %w", err)
	}

	e.Emit(core.Event{Type: "log", Message: "Split WIM files copied successfully"})
	return nil
}
