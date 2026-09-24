// Package pipeline provides a generic, typed pipeline system for multi-step operations.
// Steps share typed context and support cleanup on failure.
package pipeline

import (
	"context"
	"fmt"
	"time"

	"github.com/kyleaupton/flashit/internal/core"
	"github.com/kyleaupton/flashit/internal/logger"
)

const cleanupTimeout = 30 * time.Second

// Step is a pipeline step with typed context.
// Steps share a context struct C that can be modified during execution.
type Step[C any] interface {
	// Key returns a unique identifier for this step (used in events).
	Key() string
	// Name returns a human-readable name for UI display.
	Name() string
	// HasProgress returns true if this step emits progress events.
	HasProgress() bool
	// Run executes the step, modifying state as needed.
	Run(ctx context.Context, state *C, e core.Executor) error
}

// CleanupStep is a step that can clean up resources on failure.
// Cleanup is called in reverse order, starting with the step that failed,
// so it must cope with its own Run having stopped part way.
type CleanupStep[C any] interface {
	Step[C]
	// Cleanup releases resources allocated by this step.
	Cleanup(ctx context.Context, state *C, e core.Executor) error
}

// Pipeline holds a sequence of steps with typed context.
type Pipeline[C any] struct {
	steps []Step[C]
}

// New creates a new pipeline with the given steps.
func New[C any](steps ...Step[C]) *Pipeline[C] {
	return &Pipeline[C]{steps: steps}
}

// StepInfos returns metadata about each step for UI display.
func (p *Pipeline[C]) StepInfos() []core.StepInfo {
	infos := make([]core.StepInfo, len(p.steps))
	for i, s := range p.steps {
		infos[i] = core.StepInfo{
			Key:         s.Key(),
			Name:        s.Name(),
			HasProgress: s.HasProgress(),
		}
	}
	return infos
}

// Run executes all steps in sequence, running cleanup on failure.
// Cleanup steps are called in reverse order, from the failing step back to the first.
func (p *Pipeline[C]) Run(ctx context.Context, state *C, e core.Executor) error {
	for i, s := range p.steps {
		logger.Debug("step starting", "step", s.Name(), "key", s.Key(), "index", i)
		e.Emit(core.Event{Type: "step-start", Step: s.Key(), Message: s.Name()})

		if err := s.Run(ctx, state, e); err != nil {
			logger.Error("step failed", "step", s.Name(), "key", s.Key(), "error", err)

			p.runCleanup(ctx, state, e, i)

			e.Emit(core.Event{Type: "step-end", Step: s.Key(), Error: err.Error()})
			return fmt.Errorf("step %q failed: %w", s.Name(), err)
		}

		logger.Debug("step completed", "step", s.Name(), "key", s.Key())
		e.Emit(core.Event{Type: "step-end", Step: s.Key()})
	}
	return nil
}

// runCleanup runs cleanup for steps from index downTo 0 (reverse order).
func (p *Pipeline[C]) runCleanup(_ context.Context, state *C, e core.Executor, downTo int) {
	// Use fresh context - original may be cancelled
	cleanupCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()

	for i := downTo; i >= 0; i-- {
		if cs, ok := p.steps[i].(CleanupStep[C]); ok {
			logger.Debug("running cleanup", "step", cs.Name(), "key", cs.Key())
			if err := cs.Cleanup(cleanupCtx, state, e); err != nil {
				logger.Warn("cleanup failed", "step", cs.Name(), "key", cs.Key(), "error", err)
				// Continue cleanup even if one fails
			}
		}
	}
}

// boundPipeline wraps a Pipeline with its initial state, implementing core.Runnable.
type boundPipeline[C any] struct {
	pipeline *Pipeline[C]
	state    *C
}

// Bind creates a core.Runnable from a Pipeline and initial state.
// The state is shared across all steps during execution.
func Bind[C any](p *Pipeline[C], state *C) core.Runnable {
	return &boundPipeline[C]{pipeline: p, state: state}
}

// StepInfos returns metadata about steps for UI display.
func (b *boundPipeline[C]) StepInfos() []core.StepInfo {
	return b.pipeline.StepInfos()
}

// Run executes the bound pipeline with its captured state.
func (b *boundPipeline[C]) Run(ctx context.Context, e core.Executor) error {
	return b.pipeline.Run(ctx, b.state, e)
}
