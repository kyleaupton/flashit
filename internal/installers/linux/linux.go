package linux

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"

	"github.com/kyleaupton/flashit/internal/core"
	"github.com/kyleaupton/flashit/internal/drives"
	linuxsteps "github.com/kyleaupton/flashit/internal/installers/linux/steps"
	"github.com/kyleaupton/flashit/internal/iso"
	"github.com/kyleaupton/flashit/internal/pipeline"
	"github.com/kyleaupton/flashit/internal/priv"
)

// Linux is a generic installer for any hybrid ISO Linux distribution.
// It validates that the provided ISO is a hybrid image (can be written
// directly to USB) and creates a plan to write it using platform-native tools.
type Linux struct{}

func (l Linux) ID() string   { return "linux" }
func (l Linux) Name() string { return "Linux" }

func (l Linux) Targets() []core.Target {
	return []core.Target{{Family: core.OSLinux}}
}

func (l Linux) Plan(ctx context.Context, req core.CreateRequest) (*core.Plan, error) {
	// Check platform support
	if !drives.IsSupported() {
		return nil, errors.New("Linux USB creation is not supported on this platform")
	}

	// Skip ISO validation in dry-run mode (file may not exist)
	if !core.DryRun {
		// Validate that the ISO is a hybrid image
		if err := iso.ValidateHybrid(req.Source.Local); err != nil {
			if errors.Is(err, iso.ErrNotISO9660) {
				return nil, fmt.Errorf("invalid ISO file: %w", err)
			}
			if errors.Is(err, iso.ErrNotHybrid) {
				return nil, fmt.Errorf("this ISO cannot be written directly to USB (non-hybrid image): %w", err)
			}
			if errors.Is(err, iso.ErrFileTooSmall) {
				return nil, fmt.Errorf("file too small to be a valid ISO: %w", err)
			}
			return nil, fmt.Errorf("failed to validate ISO: %w", err)
		}
	}

	state := &linuxsteps.FlashContext{
		ISOPath:    req.Source.Local,
		TargetDisk: req.DriveID,
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
