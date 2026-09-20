package steps

import (
	"context"
	"os"
	"time"

	"github.com/kyleaupton/flashit/internal/core"
	"github.com/kyleaupton/flashit/internal/pipeline"
)

// Cleanup removes temporary files created during the pipeline.
// This step always succeeds - cleanup errors are logged but not fatal.
type Cleanup struct{}

func (Cleanup) Key() string       { return "cleanup" }
func (Cleanup) Name() string      { return "Cleaning up" }
func (Cleanup) HasProgress() bool { return false }

func (Cleanup) Run(ctx context.Context, state *FlashContext, e core.Executor) error {
	if core.DryRun {
		return pipeline.Simulate(ctx, e, 300*time.Millisecond, 2)
	}

	if state.TempISOPath != "" {
		e.Emit(core.Event{Type: "log", Message: "Removing temporary ISO..."})
		if err := os.Remove(state.TempISOPath); err != nil {
			e.Emit(core.Event{Type: "log", Message: "Warning: failed to remove temp ISO: " + err.Error()})
			// Don't fail the step for cleanup errors
		}
		state.TempISOPath = ""
	}
	return nil
}
