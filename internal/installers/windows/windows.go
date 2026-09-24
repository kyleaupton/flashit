package windows

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/kyleaupton/flashit/internal/core"
	"github.com/kyleaupton/flashit/internal/drives"
	winsteps "github.com/kyleaupton/flashit/internal/installers/windows/steps"
	"github.com/kyleaupton/flashit/internal/pipeline"
	"github.com/kyleaupton/flashit/internal/priv"
	"github.com/kyleaupton/flashit/internal/sources"
)

// defaultVolumeName matches Microsoft's Media Creation Tool.
const defaultVolumeName = "ESD-USB"

// Windows is an installer for Windows ISOs.
// Windows ISOs are not hybrid images, so they cannot be written directly to USB.
// Instead, we format the USB as FAT32 and copy the files, splitting large WIM
// files if necessary (FAT32 has a 4GB file size limit).
type Windows struct{}

func (w Windows) ID() string   { return "windows" }
func (w Windows) Name() string { return "Windows" }

func (w Windows) Plan(ctx context.Context, src sources.SourceInfo, drive drives.Drive) (*core.Plan, error) {
	if !drives.IsSupported() {
		return nil, errors.New("Windows USB creation is not supported on this platform")
	}
	if src.Kind != sources.WindowsISO {
		return nil, fmt.Errorf("%s has no Windows sources", src.Path)
	}
	if uint64(src.Size) > drive.SizeBytes {
		return nil, fmt.Errorf("image is %d bytes but the drive holds %d", src.Size, drive.SizeBytes)
	}

	state := &winsteps.FlashContext{
		ISOPath:    src.Path,
		TargetDisk: drive.Device,
		VolumeName: defaultVolumeName,
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
		winsteps.OpenSource{},
		winsteps.FormatUSB{},
		winsteps.AnalyzeWim{},
		winsteps.CopyFiles{},
		winsteps.SplitWim{},
		winsteps.Finalize{},
	)

	runnable := pipeline.Bind(p, state)

	return &core.Plan{
		ID:        "plan-windows",
		Name:      "Windows USB",
		Runnable:  runnable,
		StepInfos: runnable.StepInfos(),
	}, nil
}
