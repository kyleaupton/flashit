package steps

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/google/uuid"

	"github.com/kyleaupton/flashit/internal/core"
	"github.com/kyleaupton/flashit/internal/fs"
	"github.com/kyleaupton/flashit/internal/pipeline"
)

// Prepare clones the ISO to a neutral location to bypass TCC restrictions.
// The privileged helper cannot read TCC-protected directories (like ~/Downloads)
// even when running as root. APFS clone is near-instant for large files.
//
// This is only needed on macOS. On other platforms the privileged helper can
// read the source ISO directly, so Prepare is a no-op and Write uses the
// original ISO path. (Cloning on Linux would also fail when TempDir is a small
// tmpfs: a multi-GB ISO won't fit in RAM.)
type Prepare struct{}

func (Prepare) Key() string         { return "preparing" }
func (Prepare) Name() string        { return "Preparing ISO" }
func (Prepare) HasProgress() bool   { return false }

func (Prepare) Run(ctx context.Context, state *FlashContext, e core.Executor) error {
	if core.DryRun {
		return pipeline.Simulate(ctx, e, 1*time.Second, 5)
	}

	// Only macOS needs the ISO clone (TCC workaround). Elsewhere the helper
	// reads the source ISO directly, so skip it and let Write use ISOPath.
	if runtime.GOOS != "darwin" {
		return nil
	}

	e.Emit(core.Event{Type: "log", Message: "Preparing ISO..."})

	tempPath := filepath.Join(fs.TempISODir, uuid.New().String()+".iso")
	if err := fs.CloneFile(state.ISOPath, tempPath); err != nil {
		return fmt.Errorf("failed to prepare ISO: %w", err)
	}

	state.TempISOPath = tempPath
	e.Emit(core.Event{Type: "log", Message: "ISO prepared"})
	return nil
}

// Cleanup removes the cloned ISO file.
func (Prepare) Cleanup(_ context.Context, state *FlashContext, e core.Executor) error {
	if state.TempISOPath != "" {
		e.Emit(core.Event{Type: "log", Message: "Cleaning up temporary ISO..."})
		if err := os.Remove(state.TempISOPath); err != nil && !os.IsNotExist(err) {
			e.Emit(core.Event{Type: "log", Message: "Warning: failed to remove temp ISO: " + err.Error()})
		}
		state.TempISOPath = ""
	}
	return nil
}
