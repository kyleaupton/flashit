package steps

import (
	"context"
	"time"

	"github.com/kyleaupton/flashit/internal/core"
	"github.com/kyleaupton/flashit/internal/drives"
	"github.com/kyleaupton/flashit/internal/pipeline"
)

// Finalize closes the ISO and ejects the USB.
type Finalize struct{}

func (Finalize) Key() string       { return "finalizing" }
func (Finalize) Name() string      { return "Finalizing" }
func (Finalize) HasProgress() bool { return false }

func (Finalize) Run(ctx context.Context, state *FlashContext, e core.Executor) error {
	if core.DryRun {
		return pipeline.Simulate(ctx, e, 500*time.Millisecond, 2)
	}

	state.releaseSource(ctx, e)

	e.Emit(core.Event{Type: core.EventLog, Message: "Ejecting USB..."})
	if err := drives.Eject(ctx, state.TargetDisk); err != nil {
		e.Emit(core.Event{Type: core.EventLog, Message: "Warning: eject failed: " + err.Error()})
	}

	e.Emit(core.Event{Type: core.EventLog, Message: "Windows USB created successfully!"})
	return nil
}
