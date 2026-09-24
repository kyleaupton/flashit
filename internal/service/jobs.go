package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/kyleaupton/flashit/internal/core"
	"github.com/kyleaupton/flashit/internal/drives"
	"github.com/kyleaupton/flashit/internal/eventbus"
	"github.com/kyleaupton/flashit/internal/installers/linux"
	"github.com/kyleaupton/flashit/internal/installers/windows"
	"github.com/kyleaupton/flashit/internal/jobs"
	"github.com/kyleaupton/flashit/internal/sources"
)

type StartJobRequest struct {
	SourcePath string
	DriveID    string
}

type StartJobResponse struct {
	JobID     string          `json:"jobId"`
	StepInfos []core.StepInfo `json:"stepInfos"`
}

type JobsService struct {
	mgr        *jobs.Manager
	installers map[sources.Kind]core.Installer
}

func NewJobsService() *JobsService {
	svc := &JobsService{
		installers: map[sources.Kind]core.Installer{
			sources.LinuxISO:   linux.Linux{},
			sources.WindowsISO: windows.Windows{},
		},
	}
	svc.mgr = jobs.NewManager(func(ev core.Event) {
		eventbus.Emit("job:event", ev)
	})
	return svc
}

// StartJob probes the source, picks the installer by what it found, and
// refuses a drive the OS does not list as removable before planning.
func (s *JobsService) StartJob(ctx context.Context, req StartJobRequest) (StartJobResponse, error) {
	// Checked before Plan, whose EnsureReady pings the helper: mid-op the
	// helper answers busy and the client would retire the running job's
	// session. Enqueue checks again under the lock.
	if s.mgr.Active() {
		return StartJobResponse{}, jobs.ErrJobActive
	}
	if req.SourcePath == "" {
		return StartJobResponse{}, errors.New("source path is required")
	}
	if req.DriveID == "" {
		return StartJobResponse{}, errors.New("drive is required")
	}

	src, err := sources.Probe(req.SourcePath)
	if err != nil {
		return StartJobResponse{}, err
	}
	inst, ok := s.installers[src.Kind]
	if !ok {
		return StartJobResponse{}, fmt.Errorf("cannot flash this image: %s", src.Reason)
	}

	drive, err := findRemovable(ctx, req.DriveID)
	if err != nil {
		return StartJobResponse{}, err
	}

	plan, err := inst.Plan(ctx, src, drive)
	if err != nil {
		return StartJobResponse{}, err
	}
	// Use background context for the job - the request context gets cancelled
	// when the RPC call returns, but the job runs asynchronously
	jobID, err := s.mgr.Enqueue(context.Background(), plan)
	if err != nil {
		return StartJobResponse{}, err
	}
	return StartJobResponse{
		JobID:     jobID,
		StepInfos: plan.StepInfos,
	}, nil
}

func findRemovable(ctx context.Context, device string) (drives.Drive, error) {
	removable, err := drives.ListRemovable(ctx)
	if err != nil {
		return drives.Drive{}, fmt.Errorf("failed to list removable drives: %w", err)
	}
	for _, d := range removable {
		if d.Device == device {
			return d, nil
		}
	}
	return drives.Drive{}, fmt.Errorf("%s is not a removable drive", device)
}

// CancelJob cancels a running job by ID.
// Returns true if the job was found and cancelled, false if not found or already completed.
func (s *JobsService) CancelJob(jobID string) bool {
	return s.mgr.Cancel(jobID)
}
