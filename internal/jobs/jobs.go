package jobs

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/kyleaupton/flashit/internal/core"
	"github.com/kyleaupton/flashit/internal/logger"
	"github.com/kyleaupton/flashit/internal/proto"
)

type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

type Job struct {
	ID        string
	Plan      *core.Plan
	Status    Status
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ErrJobActive is returned by Enqueue while a job is pending or running.
// The helper serves one op at a time and the UI shows one job, so a second
// one is refused rather than queued.
var ErrJobActive = errors.New("a job is already running")

// ErrBlocked is returned by Enqueue while Block holds the manager, which
// the updater does from the moment it starts restarting the app.
var ErrBlocked = errors.New("FlashIt is restarting to install an update")

type Manager struct {
	mu          sync.Mutex
	jobs        map[string]*Job
	cancelFuncs map[string]context.CancelFunc
	emit        func(ev core.Event)
	blocked     bool
}

func NewManager(emit func(ev core.Event)) *Manager {
	return &Manager{
		jobs:        make(map[string]*Job),
		cancelFuncs: make(map[string]context.CancelFunc),
		emit:        emit,
	}
}

// Cancel cancels a running job by ID.
// Returns true if the job was found and cancelled, false if not found or already completed.
func (m *Manager) Cancel(jobID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	job, ok := m.jobs[jobID]
	if !ok {
		logger.Warn("cancel requested for unknown job", "jobID", jobID)
		return false
	}

	// Only cancel if still running
	if job.Status != StatusRunning && job.Status != StatusPending {
		logger.Info("cancel requested for completed job", "jobID", jobID, "status", job.Status)
		return false
	}

	cancel, ok := m.cancelFuncs[jobID]
	if !ok {
		logger.Warn("no cancel func for job", "jobID", jobID)
		return false
	}

	logger.Info("cancelling job", "jobID", jobID)
	cancel()
	return true
}

// Busy is ErrJobActive while a job is pending or running, ErrBlocked while
// jobs are blocked, and nil when a new job may start.
func (m *Manager) Busy() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.busy()
}

func (m *Manager) busy() error {
	if m.active() {
		return ErrJobActive
	}
	if m.blocked {
		return ErrBlocked
	}
	return nil
}

// Block refuses every new job until release is called. It fails when a job
// is pending or running, or jobs are already blocked, so whoever holds it
// knows no disk work is under way or can start.
func (m *Manager) Block() (release func(), err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.busy(); err != nil {
		return nil, err
	}
	m.blocked = true
	return sync.OnceFunc(func() {
		m.mu.Lock()
		m.blocked = false
		m.mu.Unlock()
	}), nil
}

func (m *Manager) active() bool {
	for _, j := range m.jobs {
		if j.Status == StatusPending || j.Status == StatusRunning {
			return true
		}
	}
	return false
}

func (m *Manager) Enqueue(ctx context.Context, plan *core.Plan) (string, error) {
	if plan == nil || plan.Runnable == nil {
		return "", errors.New("plan has no runnable pipeline")
	}

	// The passed ctx may be cancelled when the RPC returns; the job outlives it.
	jobCtx, cancel := context.WithCancel(context.Background())

	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.busy(); err != nil {
		cancel()
		return "", err
	}
	id := uuid.NewString()
	job := &Job{ID: id, Plan: plan, Status: StatusPending, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	m.jobs[id] = job
	m.cancelFuncs[id] = cancel

	go m.run(jobCtx, job)
	return id, nil
}

type execAdapter struct {
	emit  func(ev core.Event)
	jobID string
}

func (e execAdapter) Emit(ev core.Event) { ev.JobID = e.jobID; e.emit(ev) }

// cleanupJob removes the cancel func for a completed job.
func (m *Manager) cleanupJob(jobID string) {
	m.mu.Lock()
	delete(m.cancelFuncs, jobID)
	m.mu.Unlock()
}

func (m *Manager) run(ctx context.Context, job *Job) {
	defer m.cleanupJob(job.ID)

	stepInfos := job.Plan.Runnable.StepInfos()
	logger.Info("job started", "jobID", job.ID, "steps", len(stepInfos))

	m.mu.Lock()
	job.Status = StatusRunning
	job.UpdatedAt = time.Now()
	m.mu.Unlock()
	m.emit(core.Event{JobID: job.ID, Type: "state", Message: string(job.Status)})

	e := execAdapter{emit: m.emit, jobID: job.ID}

	err := job.Plan.Runnable.Run(ctx, e)
	if err != nil {
		if ctx.Err() == context.Canceled {
			logger.Info("job cancelled", "jobID", job.ID)
			m.mu.Lock()
			job.Status = StatusCancelled
			job.UpdatedAt = time.Now()
			m.mu.Unlock()
			m.emit(core.Event{JobID: job.ID, Type: "state", Message: string(job.Status)})
			return
		}

		logger.Error("job failed", "jobID", job.ID, "error", err)
		m.mu.Lock()
		job.Status = StatusFailed
		job.UpdatedAt = time.Now()
		m.mu.Unlock()
		m.emit(core.Event{JobID: job.ID, Type: "state", Message: string(job.Status), Error: err.Error(), Code: errorCode(err)})
		return
	}

	logger.Info("job completed", "jobID", job.ID)
	m.mu.Lock()
	job.Status = StatusSucceeded
	job.UpdatedAt = time.Now()
	m.mu.Unlock()
	m.emit(core.Event{JobID: job.ID, Type: "state", Message: string(job.Status)})
}

// errorCode is the helper's code behind err, or empty when it did not come
// from the helper.
func errorCode(err error) string {
	var pe *proto.Error
	if errors.As(err, &pe) {
		return string(pe.Code)
	}
	return ""
}
