package steps

import (
	"context"
	"time"

	"github.com/kyleaupton/flashit/internal/core"
	"github.com/kyleaupton/flashit/internal/drives"
	"github.com/kyleaupton/flashit/internal/pipeline"
	"github.com/kyleaupton/flashit/internal/priv"
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
	if state.HelperMounts {
		ejectThroughHelper(ctx, state, e)
	} else if err := drives.Eject(ctx, state.TargetDisk); err != nil {
		e.Emit(core.Event{Type: core.EventLog, Message: "Warning: eject failed: " + err.Error()})
	}

	e.Emit(core.Event{Type: core.EventLog, Message: "Windows USB created successfully!"})
	return nil
}

const (
	busyWarning = "All files were written, but the stick is still in use and could not be ejected. " +
		"Close anything using it, then eject it before removing it."
	ejectWarning = "All files were written, but the stick could not be ejected. " +
		"Eject it before removing it."
)

// ejectThroughHelper leaves the job succeeded whatever happens: the files
// are on the stick. Any failure is a warning, since only the helper can
// unmount the volume it mounted as root; device_busy gets the copy that
// tells the user what to close.
func ejectThroughHelper(ctx context.Context, state *FlashContext, e core.Executor) {
	err := state.PrivService.Disk().Eject(ctx, state.TargetDisk)
	switch {
	case err == nil:
		e.Emit(core.Event{Type: core.EventLog, Message: "USB ejected"})
	case priv.IsDeviceBusy(err):
		e.Emit(core.Event{Type: core.EventWarning, Message: busyWarning})
	default:
		e.Emit(core.Event{Type: core.EventLog, Message: "Eject failed: " + err.Error()})
		e.Emit(core.Event{Type: core.EventWarning, Message: ejectWarning})
	}
}
