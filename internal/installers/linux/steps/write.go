package steps

import (
	"context"
	"fmt"
	"time"

	"github.com/kyleaupton/flashit/internal/core"
	"github.com/kyleaupton/flashit/internal/pipeline"
)

// Write writes the ISO to the target disk using the privileged service.
// This step uses Disk Arbitration to claim exclusive access and direct I/O.
type Write struct{}

func (Write) Key() string       { return "writing-iso" }
func (Write) Name() string      { return "Writing ISO to USB" }
func (Write) HasProgress() bool { return true }

func (Write) Run(ctx context.Context, state *FlashContext, e core.Executor) error {
	if core.DryRun {
		return pipeline.Simulate(ctx, e, 8*time.Second, 20)
	}

	e.Emit(core.Event{Type: "log", Message: "Starting ISO write (this may take several minutes)..."})

	// Use the temp path if available (prepared by Prepare step), otherwise use original
	isoPath := state.TempISOPath
	if isoPath == "" {
		isoPath = state.ISOPath
	}

	// Progress callback to emit events during write
	progress := func(bytesWritten, totalBytes uint64) {
		if totalBytes > 0 {
			percent := float64(bytesWritten) * 100.0 / float64(totalBytes)
			e.Emit(core.Event{
				Type:    "progress",
				Percent: percent,
				Message: fmt.Sprintf("%.1f / %.1f GB", float64(bytesWritten)/1e9, float64(totalBytes)/1e9),
			})
		}
	}

	if err := state.PrivService.Disk().WriteISO(ctx, isoPath, state.TargetDisk, progress); err != nil {
		return fmt.Errorf("failed to write ISO: %w", err)
	}

	e.Emit(core.Event{Type: "log", Message: "ISO written successfully"})
	return nil
}
