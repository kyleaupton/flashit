package linux

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/kyleaupton/flashit/internal/core"
	"github.com/kyleaupton/flashit/internal/drives"
	linuxsteps "github.com/kyleaupton/flashit/internal/installers/linux/steps"
	"github.com/kyleaupton/flashit/internal/pipeline"
	"github.com/kyleaupton/flashit/internal/priv"
	"github.com/kyleaupton/flashit/internal/sources"
)

// Linux writes any hybrid ISO raw to the drive.
type Linux struct{}

func (l Linux) ID() string   { return "linux" }
func (l Linux) Name() string { return "Linux" }

func (l Linux) Plan(ctx context.Context, src sources.SourceInfo, drive drives.Drive) (*core.Plan, error) {
	if !drives.IsSupported() {
		return nil, errors.New("Linux USB creation is not supported on this platform")
	}
	if !core.DryRun && src.Kind != sources.LinuxISO {
		return nil, fmt.Errorf("%s is not a hybrid Linux ISO", src.Path)
	}
	if uint64(src.Size) > drive.SizeBytes {
		return nil, fmt.Errorf("image is %d bytes but the drive holds %d", src.Size, drive.SizeBytes)
	}

	state := &linuxsteps.FlashContext{
		ISOPath:    src.Path,
		TargetDisk: drive.Device,
	}

	if !core.DryRun {
		if strings.HasSuffix(drive.Device, "disk0") {
			return nil, errors.New("refusing to target /dev/disk0")
		}
		svc := priv.NewService()
		if err := svc.EnsureReady(ctx); err != nil {
			return nil, fmt.Errorf("failed to initialize privileged service: %w", err)
		}
		state.PrivService = svc
	}

	p := pipeline.New(
		linuxsteps.Prepare{},
		linuxsteps.Unmount{},
		linuxsteps.Write{},
		linuxsteps.Eject{},
	)

	runnable := pipeline.Bind(p, state)

	return &core.Plan{
		ID:        "plan-linux",
		Name:      "Linux USB",
		Runnable:  runnable,
		StepInfos: runnable.StepInfos(),
	}, nil
}
