package steps

import (
	"context"
	"fmt"
	"time"

	"github.com/kyleaupton/flashit/internal/core"
	"github.com/kyleaupton/flashit/internal/drives"
	"github.com/kyleaupton/flashit/internal/pipeline"
)

// FormatUSB formats the target disk as FAT32.
type FormatUSB struct{}

func (FormatUSB) Key() string       { return "formatting" }
func (FormatUSB) Name() string      { return "Formatting USB as FAT32" }
func (FormatUSB) HasProgress() bool { return false }

func (FormatUSB) Run(ctx context.Context, state *FlashContext, e core.Executor) error {
	if core.DryRun {
		return pipeline.Simulate(ctx, e, 2*time.Second, 5)
	}

	e.Emit(core.Event{Type: "log", Message: "Formatting USB as FAT32..."})

	e.Emit(core.Event{Type: core.EventAuthorizing})
	if err := state.PrivService.Disk().FormatDisk(ctx, state.TargetDisk, "FAT32", state.VolumeName); err != nil {
		return fmt.Errorf("failed to format USB: %w", err)
	}

	e.Emit(core.Event{Type: "log", Message: "USB formatted successfully"})

	// Wait for the system to mount the newly formatted volume
	mountPoint, err := drives.WaitForMount(ctx, state.TargetDisk, 10*time.Second)
	if err != nil {
		return fmt.Errorf("failed to find USB mount point: %w", err)
	}

	state.USBMountPath = mountPoint
	e.Emit(core.Event{Type: "log", Message: fmt.Sprintf("USB mounted at %s", mountPoint)})
	return nil
}

// Cleanup unmounts a volume the helper mounted, so a failed or cancelled job
// never leaves the stick mounted as root. Cleanups run in reverse, so
// SplitWim has removed its parts from the volume by now.
func (FormatUSB) Cleanup(ctx context.Context, state *FlashContext, e core.Executor) error {
	if core.DryRun || !state.HelperMounts || state.PrivService == nil {
		return nil
	}
	e.Emit(core.Event{Type: core.EventLog, Message: "Unmounting USB..."})
	if err := state.PrivService.Disk().Unmount(ctx, state.TargetDisk); err != nil {
		e.Emit(core.Event{Type: core.EventLog, Message: "Warning: unmount failed: " + err.Error()})
		return err
	}
	return nil
}
