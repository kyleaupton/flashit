package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kyleaupton/flashit/internal/core"
	"github.com/kyleaupton/flashit/internal/drives"
)

func TestStartJob_RefusesChangedDrive(t *testing.T) {
	listed := drives.Drive{
		Device:      "/dev/disk4",
		SizeBytes:   32 << 30,
		Model:       "Ultra",
		Serial:      "4C530001",
		IsRemovable: true,
	}
	drives.SetProvider(drives.MockProvider{Drives: []drives.Drive{listed}})
	// Dry run lets any readable file through the probe; the drive check
	// comes before anything is simulated.
	core.DryRun = true
	t.Cleanup(func() { core.DryRun = false })

	image := filepath.Join(t.TempDir(), "image.iso")
	if err := os.WriteFile(image, make([]byte, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	seen := StartJobRequest{SourcePath: image, DriveID: listed.Device, SizeBytes: listed.SizeBytes, Model: listed.Model, Serial: listed.Serial}

	for name, change := range map[string]func(*StartJobRequest){
		"size":   func(r *StartJobRequest) { r.SizeBytes = 64 << 30 },
		"model":  func(r *StartJobRequest) { r.Model = "DataTraveler" },
		"serial": func(r *StartJobRequest) { r.Serial = "4C530002" },
	} {
		t.Run(name, func(t *testing.T) {
			req := seen
			change(&req)
			_, err := NewJobsService().StartJob(context.Background(), req)
			if !errors.Is(err, ErrDriveChanged) {
				t.Fatalf("StartJob = %v, want ErrDriveChanged", err)
			}
		})
	}

	t.Run("unlisted", func(t *testing.T) {
		req := seen
		req.DriveID = "/dev/disk5"
		if _, err := NewJobsService().StartJob(context.Background(), req); err == nil || errors.Is(err, ErrDriveChanged) {
			t.Fatalf("StartJob = %v, want a not-removable refusal", err)
		}
	})

	t.Run("unchanged", func(t *testing.T) {
		d, err := findRemovable(context.Background(), seen)
		if err != nil || d.Device != listed.Device {
			t.Fatalf("findRemovable = %+v, %v", d, err)
		}
	})
}
