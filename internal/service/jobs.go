package service

import (
	"context"
	"errors"
	"github.com/kyleaupton/flashit/internal/core"
	"github.com/kyleaupton/flashit/internal/eventbus"
	"github.com/kyleaupton/flashit/internal/installers/linux"
	"github.com/kyleaupton/flashit/internal/installers/windows"
	"github.com/kyleaupton/flashit/internal/jobs"
	"os"
)

type InstallerMeta struct {
	ID      string
	Name    string
	Targets []core.Target
}

type StartJobRequest struct {
	InstallerID string
	SourceLocal string
	DriveID     string
}

type StartJobResponse struct {
	JobID     string          `json:"jobId"`
	StepInfos []core.StepInfo `json:"stepInfos"`
}

type JobsService struct {
	mgr        *jobs.Manager
	installers map[string]core.Installer
}

func NewJobsService() *JobsService {
	svc := &JobsService{
		installers: map[string]core.Installer{},
	}
	// register generic Linux installer
	l := linux.Linux{}
	svc.installers[l.ID()] = l

	// register Windows installer
	w := windows.Windows{}
	svc.installers[w.ID()] = w

	svc.mgr = jobs.NewManager(func(ev core.Event) {
		eventbus.Emit("job:event", ev)
	})
	return svc
}

func (s *JobsService) ListInstallers() []InstallerMeta {
	out := []InstallerMeta{}
	for _, inst := range s.installers {
		out = append(out, InstallerMeta{ID: inst.ID(), Name: inst.Name(), Targets: inst.Targets()})
	}
	return out
}

func (s *JobsService) StartJob(ctx context.Context, req StartJobRequest) (StartJobResponse, error) {
	inst, ok := s.installers[req.InstallerID]
	if !ok {
		return StartJobResponse{}, errors.New("installer not found")
	}
	if req.SourceLocal == "" {
		return StartJobResponse{}, errors.New("source local path is required")
	}
	if _, err := os.Stat(req.SourceLocal); err != nil {
		return StartJobResponse{}, err
	}
	plan, err := inst.Plan(ctx, core.CreateRequest{Source: core.SourceSpec{Local: req.SourceLocal}, DriveID: req.DriveID})
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

// CancelJob cancels a running job by ID.
// Returns true if the job was found and cancelled, false if not found or already completed.
func (s *JobsService) CancelJob(jobID string) bool {
	return s.mgr.Cancel(jobID)
}
