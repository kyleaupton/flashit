package jobs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kyleaupton/flashit/internal/core"
)

// blockingRunnable runs until released or its context ends.
type blockingRunnable struct {
	started chan struct{}
	release chan struct{}
}

func (r *blockingRunnable) StepInfos() []core.StepInfo { return nil }

func (r *blockingRunnable) Run(ctx context.Context, e core.Executor) error {
	close(r.started)
	select {
	case <-r.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func newBlockingPlan() (*core.Plan, *blockingRunnable) {
	r := &blockingRunnable{started: make(chan struct{}), release: make(chan struct{})}
	return &core.Plan{ID: "p", Runnable: r}, r
}

// stateEvents lets a test wait for a terminal "state" event.
type stateEvents struct {
	done chan struct{}
}

func newStateEvents() *stateEvents { return &stateEvents{done: make(chan struct{}, 8)} }

func (s *stateEvents) emit(ev core.Event) {
	if ev.Type != core.EventState {
		return
	}
	switch ev.Message {
	case string(StatusSucceeded), string(StatusFailed), string(StatusCancelled):
		s.done <- struct{}{}
	}
}

func (s *stateEvents) wait(t *testing.T) {
	t.Helper()
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
		t.Fatal("job did not finish")
	}
}

func TestEnqueue_RefusesSecondJobWhileRunning(t *testing.T) {
	events := newStateEvents()
	m := NewManager(events.emit)

	plan, r := newBlockingPlan()
	id, err := m.Enqueue(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	<-r.started
	if !m.Active() {
		t.Fatal("Active should be true while the job runs")
	}

	second, _ := newBlockingPlan()
	if _, err := m.Enqueue(context.Background(), second); !errors.Is(err, ErrJobActive) {
		t.Fatalf("second Enqueue: got %v, want ErrJobActive", err)
	}

	close(r.release)
	events.wait(t)
	if m.Active() {
		t.Fatal("Active should be false after the job finished")
	}

	third, r3 := newBlockingPlan()
	id3, err := m.Enqueue(context.Background(), third)
	if err != nil {
		t.Fatalf("Enqueue after completion: %v", err)
	}
	if id3 == id {
		t.Fatal("expected a fresh job id")
	}
	<-r3.started
	close(r3.release)
}

func TestEnqueue_AllowedAfterCancel(t *testing.T) {
	events := newStateEvents()
	m := NewManager(events.emit)

	plan, r := newBlockingPlan()
	id, err := m.Enqueue(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	<-r.started
	if !m.Cancel(id) {
		t.Fatal("Cancel returned false for a running job")
	}
	events.wait(t)

	next, r2 := newBlockingPlan()
	if _, err := m.Enqueue(context.Background(), next); err != nil {
		t.Fatalf("Enqueue after cancel: %v", err)
	}
	<-r2.started
	close(r2.release)
}
