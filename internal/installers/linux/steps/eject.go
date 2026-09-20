package steps

import (
	"context"
	"time"

	"github.com/kyleaupton/flashit/internal/core"
	"github.com/kyleaupton/flashit/internal/pipeline"
)

// Eject ejects the target disk after writing is complete.
type Eject struct{}

func (Eject) Key() string       { return "ejecting" }
func (Eject) Name() string      { return "Ejecting disk" }
func (Eject) HasProgress() bool { return false }

func (Eject) Run(ctx context.Context, state *FlashContext, e core.Executor) error {
	if core.DryRun {
		return pipeline.Simulate(ctx, e, 500*time.Millisecond, 2)
	}

	e.Emit(core.Event{Type: "log", Message: "Ejecting disk..."})

	if err := state.PrivService.Disk().Eject(ctx, state.TargetDisk); err != nil {
		// Eject failure is not fatal - just log it
		e.Emit(core.Event{Type: "log", Message: "Warning: eject failed: " + err.Error()})
		return nil
	}

	e.Emit(core.Event{Type: "log", Message: "Disk ejected"})
	return nil
}
