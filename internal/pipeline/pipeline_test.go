package pipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/kyleaupton/flashit/internal/core"
)

// testContext is a simple context for testing.
type testContext struct {
	Values []string
}

// mockStep is a step that appends a value to the context.
type mockStep struct {
	key   string
	value string
}

func (m mockStep) Key() string       { return m.key }
func (m mockStep) Name() string      { return "Mock: " + m.value }
func (m mockStep) HasProgress() bool { return false }
func (m mockStep) Run(ctx context.Context, state *testContext, e core.Executor) error {
	state.Values = append(state.Values, m.value)
	return nil
}

// failingStep fails with a given error.
type failingStep struct {
	key string
	err error
}

func (f failingStep) Key() string       { return f.key }
func (f failingStep) Name() string      { return "Failing step" }
func (f failingStep) HasProgress() bool { return false }
func (f failingStep) Run(ctx context.Context, state *testContext, e core.Executor) error {
	return f.err
}

// cleanupStep tracks cleanup calls.
type cleanupStep struct {
	key       string
	value     string
	cleanedUp *bool
}

func (c cleanupStep) Key() string       { return c.key }
func (c cleanupStep) Name() string      { return "Cleanup: " + c.value }
func (c cleanupStep) HasProgress() bool { return false }
func (c cleanupStep) Run(ctx context.Context, state *testContext, e core.Executor) error {
	state.Values = append(state.Values, c.value)
	return nil
}
func (c cleanupStep) Cleanup(ctx context.Context, state *testContext, e core.Executor) error {
	*c.cleanedUp = true
	state.Values = append(state.Values, "cleanup-"+c.value)
	return nil
}

// failingCleanupStep fails in Run and records that its Cleanup still ran.
type failingCleanupStep struct {
	key       string
	cleanedUp *bool
}

func (f failingCleanupStep) Key() string       { return f.key }
func (f failingCleanupStep) Name() string      { return "Failing cleanup step" }
func (f failingCleanupStep) HasProgress() bool { return false }
func (f failingCleanupStep) Run(ctx context.Context, state *testContext, e core.Executor) error {
	state.Values = append(state.Values, "partial")
	return errors.New("failed part way")
}
func (f failingCleanupStep) Cleanup(ctx context.Context, state *testContext, e core.Executor) error {
	*f.cleanedUp = true
	state.Values = append(state.Values, "cleanup-partial")
	return nil
}

// mockExecutor captures emitted events.
type mockExecutor struct {
	events []core.Event
}

func (m *mockExecutor) Emit(ev core.Event) {
	m.events = append(m.events, ev)
}

func TestPipeline_Run_Success(t *testing.T) {
	state := &testContext{}
	e := &mockExecutor{}

	p := New[testContext](
		mockStep{key: "step-1", value: "A"},
		mockStep{key: "step-2", value: "B"},
		mockStep{key: "step-3", value: "C"},
	)

	err := p.Run(context.Background(), state, e)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(state.Values) != 3 {
		t.Fatalf("expected 3 values, got %d", len(state.Values))
	}
	if state.Values[0] != "A" || state.Values[1] != "B" || state.Values[2] != "C" {
		t.Fatalf("unexpected values: %v", state.Values)
	}
}

func TestPipeline_Run_StepFails(t *testing.T) {
	state := &testContext{}
	e := &mockExecutor{}

	expectedErr := errors.New("step failed")
	p := New[testContext](
		mockStep{key: "step-1", value: "A"},
		failingStep{key: "step-2", err: expectedErr},
		mockStep{key: "step-3", value: "C"},
	)

	err := p.Run(context.Background(), state, e)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected %v, got %v", expectedErr, err)
	}

	// Only first step should have run
	if len(state.Values) != 1 {
		t.Fatalf("expected 1 value, got %d: %v", len(state.Values), state.Values)
	}
	if state.Values[0] != "A" {
		t.Fatalf("expected A, got %s", state.Values[0])
	}
}

func TestPipeline_Run_CleanupOnFailure(t *testing.T) {
	state := &testContext{}
	e := &mockExecutor{}

	cleanup1 := false
	cleanup2 := false

	p := New[testContext](
		cleanupStep{key: "step-1", value: "A", cleanedUp: &cleanup1},
		cleanupStep{key: "step-2", value: "B", cleanedUp: &cleanup2},
		failingStep{key: "step-3", err: errors.New("failed")},
	)

	err := p.Run(context.Background(), state, e)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Both cleanup steps should have been called
	if !cleanup1 {
		t.Error("cleanup1 should have been called")
	}
	if !cleanup2 {
		t.Error("cleanup2 should have been called")
	}

	// Check cleanup order (reverse: B then A)
	expectedValues := []string{"A", "B", "cleanup-B", "cleanup-A"}
	if len(state.Values) != len(expectedValues) {
		t.Fatalf("expected %d values, got %d: %v", len(expectedValues), len(state.Values), state.Values)
	}
	for i, v := range expectedValues {
		if state.Values[i] != v {
			t.Errorf("expected %s at index %d, got %s", v, i, state.Values[i])
		}
	}
}

func TestPipeline_Run_CleanupIncludesFailingStep(t *testing.T) {
	state := &testContext{}
	e := &mockExecutor{}

	cleanupA := false
	cleanupFailing := false

	p := New[testContext](
		cleanupStep{key: "step-1", value: "A", cleanedUp: &cleanupA},
		failingCleanupStep{key: "step-2", cleanedUp: &cleanupFailing},
		mockStep{key: "step-3", value: "C"},
	)

	if err := p.Run(context.Background(), state, e); err == nil {
		t.Fatal("expected error, got nil")
	}
	if !cleanupFailing {
		t.Error("the failing step's cleanup should have run")
	}
	if !cleanupA {
		t.Error("cleanup of the earlier step should have run")
	}
	expected := []string{"A", "partial", "cleanup-partial", "cleanup-A"}
	if len(state.Values) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, state.Values)
	}
	for i, v := range expected {
		if state.Values[i] != v {
			t.Fatalf("expected %v, got %v", expected, state.Values)
		}
	}
}

func TestPipeline_StepInfos(t *testing.T) {
	p := New[testContext](
		mockStep{key: "step-1", value: "A"},
		mockStep{key: "step-2", value: "B"},
	)

	infos := p.StepInfos()
	if len(infos) != 2 {
		t.Fatalf("expected 2 step infos, got %d", len(infos))
	}

	if infos[0].Key != "step-1" {
		t.Errorf("expected step-1, got %s", infos[0].Key)
	}
	if infos[1].Key != "step-2" {
		t.Errorf("expected step-2, got %s", infos[1].Key)
	}
}

func TestBind_Runnable(t *testing.T) {
	state := &testContext{}
	e := &mockExecutor{}

	p := New[testContext](
		mockStep{key: "step-1", value: "X"},
		mockStep{key: "step-2", value: "Y"},
	)

	runnable := Bind(p, state)

	// Check StepInfos works through Runnable
	infos := runnable.StepInfos()
	if len(infos) != 2 {
		t.Fatalf("expected 2 step infos, got %d", len(infos))
	}

	// Run through Runnable interface
	err := runnable.Run(context.Background(), e)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(state.Values) != 2 {
		t.Fatalf("expected 2 values, got %d", len(state.Values))
	}
	if state.Values[0] != "X" || state.Values[1] != "Y" {
		t.Fatalf("unexpected values: %v", state.Values)
	}
}

func TestPipeline_ContextPassedBetweenSteps(t *testing.T) {
	// Step that sets a value
	type contextWithPath struct {
		Path string
	}

	setterStep := struct {
		Step[contextWithPath]
	}{
		Step: &funcStep[contextWithPath]{
			key:  "setter",
			name: "Set path",
			run: func(ctx context.Context, state *contextWithPath, e core.Executor) error {
				state.Path = "/tmp/test"
				return nil
			},
		},
	}

	var capturedPath string
	readerStep := &funcStep[contextWithPath]{
		key:  "reader",
		name: "Read path",
		run: func(ctx context.Context, state *contextWithPath, e core.Executor) error {
			capturedPath = state.Path
			return nil
		},
	}

	state := &contextWithPath{}
	e := &mockExecutor{}

	p := New[contextWithPath](setterStep.Step, readerStep)
	err := p.Run(context.Background(), state, e)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if capturedPath != "/tmp/test" {
		t.Fatalf("expected /tmp/test, got %s", capturedPath)
	}
}

// funcStep is a helper for inline step definitions in tests.
type funcStep[C any] struct {
	key     string
	name    string
	run     func(ctx context.Context, state *C, e core.Executor) error
	cleanup func(ctx context.Context, state *C, e core.Executor) error
}

func (f *funcStep[C]) Key() string       { return f.key }
func (f *funcStep[C]) Name() string      { return f.name }
func (f *funcStep[C]) HasProgress() bool { return false }
func (f *funcStep[C]) Run(ctx context.Context, state *C, e core.Executor) error {
	return f.run(ctx, state, e)
}
func (f *funcStep[C]) Cleanup(ctx context.Context, state *C, e core.Executor) error {
	if f.cleanup != nil {
		return f.cleanup(ctx, state, e)
	}
	return nil
}
