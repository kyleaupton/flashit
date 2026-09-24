package steps

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/kyleaupton/flashit/internal/core"
	"github.com/kyleaupton/flashit/internal/iso"
	"github.com/kyleaupton/flashit/internal/isofs"
	"github.com/kyleaupton/flashit/internal/pipeline"
)

// OpenSource makes the ISO's files readable. Where the host cannot mount
// it (Linux), or when FLASHIT_ISO_READER=go, the image is read in process
// through isofs, whose Open checks the whole tree; that runs before
// FormatUSB, so a bad image never costs the user their drive.
type OpenSource struct{}

func (OpenSource) Key() string       { return "opening-iso" }
func (OpenSource) Name() string      { return "Opening ISO" }
func (OpenSource) HasProgress() bool { return false }

func (OpenSource) Run(ctx context.Context, state *FlashContext, e core.Executor) error {
	if core.DryRun {
		return pipeline.Simulate(ctx, e, 1*time.Second, 3)
	}

	if !iso.IsMountSupported() || os.Getenv("FLASHIT_ISO_READER") == "go" {
		e.Emit(core.Event{Type: core.EventLog, Message: "Reading Windows ISO..."})
		im, err := isofs.Open(state.ISOPath)
		if err != nil {
			return err
		}
		state.Source = im
		state.closeSource = func(context.Context) { im.Close() }
		return nil
	}

	if devEntry := iso.DetachExisting(ctx, state.ISOPath); devEntry != "" {
		e.Emit(core.Event{Type: core.EventLog, Message: fmt.Sprintf("Detached stale mount at %s", devEntry)})
	}
	e.Emit(core.Event{Type: core.EventLog, Message: "Mounting Windows ISO..."})
	result, err := iso.Mount(ctx, state.ISOPath)
	if err != nil {
		return err
	}
	state.Source = os.DirFS(result.MountPath)
	state.closeSource = func(ctx context.Context) { iso.Unmount(ctx, result) }
	e.Emit(core.Event{Type: core.EventLog, Message: fmt.Sprintf("ISO mounted at %s", result.MountPath)})
	return nil
}

func (OpenSource) Cleanup(ctx context.Context, state *FlashContext, e core.Executor) error {
	state.releaseSource(ctx, e)
	return nil
}
