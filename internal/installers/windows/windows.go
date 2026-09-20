package windows

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/kyleaupton/flashit/internal/core"
	"github.com/kyleaupton/flashit/internal/drives"
	winsteps "github.com/kyleaupton/flashit/internal/installers/windows/steps"
	"github.com/kyleaupton/flashit/internal/iso"
	"github.com/kyleaupton/flashit/internal/pipeline"
	"github.com/kyleaupton/flashit/internal/priv"
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

func (w Windows) Targets() []core.Target {
	return []core.Target{{Family: core.OSWindows}}
}

func (w Windows) Plan(ctx context.Context, req core.CreateRequest) (*core.Plan, error) {
	// Check platform support
	if !iso.IsMountSupported() || !drives.IsSupported() {
		return nil, errors.New("Windows USB creation is not supported on this platform")
	}

	// Skip validation in dry-run mode (file may not exist)
	if !core.DryRun {
		// Validate the ISO exists
		if req.Source.Local == "" {
			return nil, errors.New("ISO path is required")
		}

		info, err := os.Stat(req.Source.Local)
		if err != nil {
			return nil, fmt.Errorf("cannot access ISO: %w", err)
		}
		if info.IsDir() {
			return nil, errors.New("ISO path is a directory, not a file")
		}
	}

	state := &winsteps.FlashContext{
		ISOPath:    req.Source.Local,
		TargetDisk: req.DriveID,
		VolumeName: defaultVolumeName,
	}

	// Skip validation and privileged service in dry-run mode
	if !core.DryRun {
		// Validate drive ID
		if req.DriveID == "" {
			return nil, errors.New("DriveID is required")
		}

		// Guard against targeting the boot drive (macOS specific, but safe to check everywhere)
		if strings.HasSuffix(req.DriveID, "disk0") || req.DriveID == "/dev/disk0" {
			return nil, errors.New("refusing to target /dev/disk0")
		}

		// Normalize device path for macOS
		if runtime.GOOS == "darwin" && !strings.HasPrefix(req.DriveID, "/dev/") {
			req.DriveID = "/dev/" + req.DriveID
			state.TargetDisk = req.DriveID
		}

		// Verify the drive is removable
		removable, err := drives.ListRemovable(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list removable drives: %w", err)
		}

		found := false
		for _, d := range removable {
			if d.Device == req.DriveID {
				found = true
				break
			}
		}
		if !found {
			return nil, errors.New("DriveID not recognized as removable USB drive")
		}

		// Initialize privileged service
		svc := priv.NewService()
		if err := svc.EnsureReady(ctx); err != nil {
			return nil, fmt.Errorf("failed to initialize privileged service: %w", err)
		}
		state.PrivService = svc
	}

	p := pipeline.New(
		winsteps.MountISO{},
		winsteps.FormatUSB{},
		winsteps.AnalyzeWim{},
		winsteps.CopyFiles{},
		winsteps.SplitWim{},
		winsteps.CopyWim{},
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
