package service

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/kyleaupton/flashit/internal/core"
	"github.com/kyleaupton/flashit/internal/drives"
	"github.com/kyleaupton/flashit/internal/eventbus"
	"github.com/kyleaupton/flashit/internal/installers/linux"
	"github.com/kyleaupton/flashit/internal/installers/windows"
	"github.com/kyleaupton/flashit/internal/jobs"
	"github.com/kyleaupton/flashit/internal/priv"
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
	// start serializes StartJob: Plan spawns the privileged helper (a
	// polkit prompt on Linux) before Enqueue takes its lock, so two
	// overlapping starts must not both get that far.
	start      sync.Mutex
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
	s.start.Lock()
	defer s.start.Unlock()

	// Checked before Plan, whose EnsureReady pings the helper: mid-op the
	// helper answers busy and the client would retire the running job's
	// session. Enqueue checks again under its own lock.
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
		if !core.DryRun {
			return StartJobResponse{}, fmt.Errorf("cannot flash this image: %s", src.Reason)
		}
		// Every step is simulated, so any readable file stands in for an image.
		inst = s.installers[sources.LinuxISO]
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
		// Plan already brought the helper up for this job; let it go.
		if !core.DryRun {
			priv.NewService().Shutdown(ctx)
		}
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
