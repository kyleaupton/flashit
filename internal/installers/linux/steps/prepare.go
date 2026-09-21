package steps

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/kyleaupton/flashit/internal/core"
	"github.com/kyleaupton/flashit/internal/pipeline"
)

// Prepare checks the ISO can be opened before anything touches the drive.
// The app opens the image itself and hands the helper the descriptor, so
// nothing is copied anywhere first; a file this process cannot read fails
// here, with the drive still mounted.
type Prepare struct{}

func (Prepare) Key() string       { return "preparing" }
func (Prepare) Name() string      { return "Preparing ISO" }
func (Prepare) HasProgress() bool { return false }

func (Prepare) Run(ctx context.Context, state *FlashContext, e core.Executor) error {
	if core.DryRun {
		return pipeline.Simulate(ctx, e, 1*time.Second, 5)
	}
	f, err := os.Open(state.ISOPath)
	if err != nil {
		return fmt.Errorf("cannot read ISO: %w", err)
	}
	f.Close()
	e.Emit(core.Event{Type: "log", Message: "ISO ready"})
	return nil
}
