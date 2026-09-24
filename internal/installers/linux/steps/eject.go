package steps

import (
	"context"
	"time"

	"github.com/kyleaupton/flashit/internal/core"
	"github.com/kyleaupton/flashit/internal/pipeline"
	"github.com/kyleaupton/flashit/internal/priv"
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

	e.Emit(core.Event{Type: core.EventLog, Message: "Ejecting disk..."})

	// Eject failure is not fatal: the image is written. A volume that could
	// not be unmounted is the user's to release, so that one is a warning.
	err := state.PrivService.Disk().Eject(ctx, state.TargetDisk)
	switch {
	case err == nil:
		e.Emit(core.Event{Type: core.EventLog, Message: "Disk ejected"})
	case priv.IsDeviceBusy(err):
		e.Emit(core.Event{Type: core.EventWarning, Message: busyWarning})
	default:
		e.Emit(core.Event{Type: core.EventLog, Message: "Warning: eject failed: " + err.Error()})
	}
	return nil
}

const busyWarning = "The image was written, but the stick is still in use and could not be ejected. " +
	"Close anything using it, then eject it before removing it."
