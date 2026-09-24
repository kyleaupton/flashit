package windows

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kyleaupton/flashit/internal/core"
	winsteps "github.com/kyleaupton/flashit/internal/installers/windows/steps"
	"github.com/kyleaupton/flashit/internal/pipeline"
	"github.com/kyleaupton/flashit/internal/priv/privtest"
)

// cleanupOnly stands in for a step whose Run already happened, keeping its
// real Cleanup. fail makes it the step that fails.
type cleanupOnly struct {
	pipeline.Step[winsteps.FlashContext]
	fail bool
}

func (c cleanupOnly) Run(context.Context, *winsteps.FlashContext, core.Executor) error {
	if c.fail {
		return errors.New("failed")
	}
	return nil
}

func (c cleanupOnly) Cleanup(ctx context.Context, s *winsteps.FlashContext, e core.Executor) error {
	if cs, ok := c.Step.(pipeline.CleanupStep[winsteps.FlashContext]); ok {
		return cs.Cleanup(ctx, s, e)
	}
	return nil
}

type discard struct{}

func (discard) Emit(core.Event) {}

// When the last step fails, the WIM split's parts are removed from the
// volume before FormatUSB's cleanup unmounts it.
func TestCleanupRemovesPartsBeforeUnmount(t *testing.T) {
	vol := t.TempDir()
	if err := os.Mkdir(filepath.Join(vol, "sources"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"install.swm", "install2.swm"} {
		if err := os.WriteFile(filepath.Join(vol, "sources", name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	svc := &privtest.Service{}
	var partsAtUnmount []string
	svc.Ops.OnUnmount = func(string) {
		partsAtUnmount, _ = filepath.Glob(filepath.Join(vol, "sources", "*.swm"))
	}
	state := &winsteps.FlashContext{
		TargetDisk:   "/dev/sdb",
		PrivService:  svc,
		HelperMounts: true,
		USBMountPath: vol,
		InstallWim:   "sources/install.wim",
		NeedsSplit:   true,
	}

	all := steps()
	wrapped := make([]pipeline.Step[winsteps.FlashContext], len(all))
	for i, s := range all {
		wrapped[i] = cleanupOnly{Step: s, fail: i == len(all)-1}
	}
	if err := pipeline.New(wrapped...).Run(context.Background(), state, discard{}); err == nil {
		t.Fatal("pipeline did not fail")
	}
	if len(svc.Ops.Unmounted) != 1 {
		t.Fatalf("unmounted %v", svc.Ops.Unmounted)
	}
	if len(partsAtUnmount) != 0 {
		t.Fatalf("parts still on the volume at unmount: %v", partsAtUnmount)
	}
}
