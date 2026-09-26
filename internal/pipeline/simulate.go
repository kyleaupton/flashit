package pipeline

import (
	"context"
	"time"

	"github.com/kyleaupton/flashit/internal/core"
)

// Simulate simulates a step for dry-run mode with delay and progress events.
// This allows testing the UI without performing real disk operations.
func Simulate(ctx context.Context, e core.Executor, delay time.Duration, ticks int) error {
	if ticks <= 0 {
		ticks = 10
	}

	tickDuration := delay / time.Duration(ticks)

	for i := 1; i <= ticks; i++ {
		select {
		case <-time.After(tickDuration):
			percent := float64(i) / float64(ticks) * 100
			e.Emit(core.Event{
				Type:    "progress",
				Percent: percent,
			})
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return nil
}

// SimulateTransfer is Simulate for a step that moves total bytes, and
// reports them as it goes.
func SimulateTransfer(ctx context.Context, e core.Executor, delay time.Duration, ticks int, total uint64) error {
	tickDuration := delay / time.Duration(ticks)
	for i := 1; i <= ticks; i++ {
		select {
		case <-time.After(tickDuration):
			done := total * uint64(i) / uint64(ticks)
			e.Emit(core.Event{
				Type:    core.EventProgress,
				Percent: float64(i) / float64(ticks) * 100,
				Bytes:   done,
				Total:   total,
			})
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// SimulateApproval stands in for the OS prompt before a privileged step.
func SimulateApproval(ctx context.Context, e core.Executor) error {
	e.Emit(core.Event{Type: core.EventAuthorizing})
	select {
	case <-time.After(2 * time.Second):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
