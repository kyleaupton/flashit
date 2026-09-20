package jobs

import (
	"context"
	"github.com/kyleaupton/flashit/internal/core"
	"github.com/kyleaupton/flashit/internal/logger"
	"sync"
	"time"
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
	Progress  float64
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Manager struct {
	mu          sync.Mutex
	jobs        map[string]*Job
	cancelFuncs map[string]context.CancelFunc
	emit        func(ev core.Event)
}

func NewManager(emit func(ev core.Event)) *Manager {
	return &Manager{
		jobs:        make(map[string]*Job),
		cancelFuncs: make(map[string]context.CancelFunc),
		emit:        emit,
	}
}

func (m *Manager) List() []Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Job, 0, len(m.jobs))
	for _, j := range m.jobs {
		out = append(out, *j)
	}
	return out
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

func (m *Manager) Enqueue(ctx context.Context, plan *core.Plan) (string, error) {
	// Create a cancellable context for this job
	// We don't use the passed ctx directly because it may be cancelled when the RPC returns
	jobCtx, cancel := context.WithCancel(context.Background())

	m.mu.Lock()
	id := time.Now().Format("20060102150405.000")
	job := &Job{ID: id, Plan: plan, Status: StatusPending, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	m.jobs[id] = job
	m.cancelFuncs[id] = cancel
	m.mu.Unlock()

	go m.run(jobCtx, job)
	return id, nil
}

type execAdapter struct {
	emit  func(ev core.Event)
	jobID string
}

func (e execAdapter) Emit(ev core.Event) { ev.JobID = e.jobID; e.emit(ev) }

func (m *Manager) run(ctx context.Context, job *Job) {
	// Prefer Runnable (typed pipeline) over legacy Steps
	if job.Plan.Runnable != nil {
		m.runPipeline(ctx, job)
		return
	}

	m.runLegacySteps(ctx, job)
}

// cleanupJob removes the cancel func for a completed job.
func (m *Manager) cleanupJob(jobID string) {
	m.mu.Lock()
	delete(m.cancelFuncs, jobID)
	m.mu.Unlock()
}

// runPipeline runs a job using the new typed pipeline system.
func (m *Manager) runPipeline(ctx context.Context, job *Job) {
	// Clean up cancel func when done
	defer m.cleanupJob(job.ID)

	stepInfos := job.Plan.Runnable.StepInfos()
	logger.Info("job started (pipeline)", "jobID", job.ID, "steps", len(stepInfos))

	m.mu.Lock()
	job.Status = StatusRunning
	job.UpdatedAt = time.Now()
	m.mu.Unlock()
	m.emit(core.Event{JobID: job.ID, Type: "state", Message: string(job.Status)})

	e := execAdapter{emit: m.emit, jobID: job.ID}

	err := job.Plan.Runnable.Run(ctx, e)
	if err != nil {
		// Check if this was a cancellation
		if ctx.Err() == context.Canceled {
			logger.Info("job cancelled (pipeline)", "jobID", job.ID)
			m.mu.Lock()
			job.Status = StatusCancelled
			job.UpdatedAt = time.Now()
			m.mu.Unlock()
			m.emit(core.Event{JobID: job.ID, Type: "state", Message: string(job.Status)})
			return
		}

		logger.Error("job failed (pipeline)", "jobID", job.ID, "error", err)
		m.mu.Lock()
		job.Status = StatusFailed
		job.UpdatedAt = time.Now()
		m.mu.Unlock()
		m.emit(core.Event{JobID: job.ID, Type: "state", Message: string(job.Status), Error: err.Error()})
		return
	}

	logger.Info("job completed (pipeline)", "jobID", job.ID)
	m.mu.Lock()
	job.Status = StatusSucceeded
	job.UpdatedAt = time.Now()
	m.mu.Unlock()
	m.emit(core.Event{JobID: job.ID, Type: "state", Message: string(job.Status)})
}

// runLegacySteps runs a job using the legacy []Step system.
func (m *Manager) runLegacySteps(ctx context.Context, job *Job) {
	// Clean up cancel func when done
	defer m.cleanupJob(job.ID)

	logger.Info("job started", "jobID", job.ID, "steps", len(job.Plan.Steps))

	m.mu.Lock()
	job.Status = StatusRunning
	job.UpdatedAt = time.Now()
	m.mu.Unlock()
	m.emit(core.Event{JobID: job.ID, Type: "state", Message: string(job.Status)})

	e := execAdapter{emit: m.emit, jobID: job.ID}
	total := float64(len(job.Plan.Steps))
	for i, s := range job.Plan.Steps {
		// Check for cancellation before each step
		if ctx.Err() == context.Canceled {
			logger.Info("job cancelled", "jobID", job.ID)
			m.mu.Lock()
			job.Status = StatusCancelled
			job.UpdatedAt = time.Now()
			m.mu.Unlock()
			m.emit(core.Event{JobID: job.ID, Type: "state", Message: string(job.Status)})
			return
		}

		// Use step key from StepInfos for event emission
		stepKey := ""
		if i < len(job.Plan.StepInfos) {
			stepKey = job.Plan.StepInfos[i].Key
		}

		logger.Debug("step starting", "jobID", job.ID, "step", s.Name(), "key", stepKey, "index", i)
		e.Emit(core.Event{Type: "step-start", Step: stepKey, Message: s.Name()})
		err := s.Run(ctx, e)
		if err != nil {
			// Check if this was a cancellation
			if ctx.Err() == context.Canceled {
				logger.Info("job cancelled", "jobID", job.ID)
				m.mu.Lock()
				job.Status = StatusCancelled
				job.UpdatedAt = time.Now()
				m.mu.Unlock()
				m.emit(core.Event{JobID: job.ID, Type: "state", Message: string(job.Status)})
				return
			}

			logger.Error("step failed", "jobID", job.ID, "step", s.Name(), "key", stepKey, "error", err)
			m.mu.Lock()
			job.Status = StatusFailed
			job.UpdatedAt = time.Now()
			m.mu.Unlock()
			m.emit(core.Event{JobID: job.ID, Type: "state", Message: string(job.Status), Step: stepKey, Error: err.Error()})
			return
		}
		logger.Debug("step completed", "jobID", job.ID, "step", s.Name(), "key", stepKey)
		m.mu.Lock()
		job.Progress = float64(i+1) / total
		job.UpdatedAt = time.Now()
		m.mu.Unlock()
		e.Emit(core.Event{Type: "step-end", Step: stepKey})
	}

	logger.Info("job completed", "jobID", job.ID)
	m.mu.Lock()
	job.Status = StatusSucceeded
	job.UpdatedAt = time.Now()
	m.mu.Unlock()
	m.emit(core.Event{JobID: job.ID, Type: "state", Message: string(job.Status)})
}
