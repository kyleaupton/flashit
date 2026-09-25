package update

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/updater"

	"github.com/kyleaupton/flashit/internal/core"
	"github.com/kyleaupton/flashit/internal/jobs"
)

type fakeEngine struct {
	mu         sync.Mutex
	rel        *updater.Release
	checkErr   error
	restartErr error
	checks     int
	downloads  int
	restarts   int
}

func (f *fakeEngine) Check(context.Context) (*updater.Release, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checks++
	return f.rel, f.checkErr
}

func (f *fakeEngine) DownloadAndInstall(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.downloads++
	return nil
}

func (f *fakeEngine) Restart(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restarts++
	return f.restartErr
}

func (f *fakeEngine) DownloadedPath() string { return "/tmp/wails-update-x/FlashIt.app" }

type fakeGate struct {
	err      error
	blocked  int
	released int
}

func (g *fakeGate) BlockJobs() (func(), error) {
	if g.err != nil {
		return nil, g.err
	}
	g.blocked++
	return func() { g.released++ }, nil
}

func release(v string) *updater.Release {
	return &updater.Release{Version: v, Notes: "notes"}
}

func stagedOK(string, string) error { return nil }

func TestModeFor(t *testing.T) {
	cases := []struct {
		goos, version string
		want          Mode
	}{
		{"darwin", "dev", Off},
		{"linux", "dev", Off},
		{"darwin", "", Off},
		{"darwin", "0.1.1-12-g8515f6a", Off},
		{"linux", "0.0.0-dev.42", Off},
		{"darwin", "v1.2.3", Off},
		{"darwin", "1.2.3", Install},
		{"linux", "1.2.3", Notify},
		{"windows", "1.2.3", Off},
		{"freebsd", "1.2.3", Off},
	}
	for _, c := range cases {
		if got := ModeFor(c.goos, c.version); got != c.want {
			t.Errorf("ModeFor(%q, %q) = %q, want %q", c.goos, c.version, got, c.want)
		}
	}
}

func TestNew_LeavesUpdaterUnconfigured(t *testing.T) {
	for _, c := range []struct{ goos, version string }{
		{"darwin", "dev"},
		{"linux", "dev"},
		{"windows", "1.2.3"},
	} {
		u := updater.New(&fakeHost{})
		m, err := New(u, Config{GOOS: c.goos, Version: c.version, ManifestURL: "http://127.0.0.1:1/m.json"})
		if err != nil {
			t.Fatal(err)
		}
		if m.Mode() != Off {
			t.Errorf("%s/%s: mode %q, want off", c.goos, c.version, m.Mode())
		}
		if u.State() != updater.StateUnconfigured {
			t.Errorf("%s/%s: updater was configured", c.goos, c.version)
		}
		if s := m.Check(context.Background()); s.Status != StatusIdle {
			t.Errorf("%s/%s: Check moved an off manager to %q", c.goos, c.version, s.Status)
		}
		if err := m.Restart(context.Background()); !errors.Is(err, ErrOff) {
			t.Errorf("%s/%s: Restart = %v, want ErrOff", c.goos, c.version, err)
		}
	}
}

func TestLinux_NeverDownloadsOrRestarts(t *testing.T) {
	eng := &fakeEngine{rel: release("1.2.4")}
	gate := &fakeGate{}
	var emitted []State
	m := NewManager(ManagerConfig{Mode: Notify, Current: "1.2.3", Engine: eng, Gate: gate, Emit: func(s State) { emitted = append(emitted, s) }})

	s := m.Check(context.Background())
	if s.Status != StatusAvailable || s.Version != "1.2.4" {
		t.Fatalf("state = %+v, want available 1.2.4", s)
	}
	if s.ReleaseURL != "https://github.com/kyleaupton/flashit/releases/tag/v1.2.4" {
		t.Errorf("release URL %q", s.ReleaseURL)
	}
	if err := m.Restart(context.Background()); !errors.Is(err, ErrOff) {
		t.Errorf("Restart = %v, want ErrOff", err)
	}
	m.Check(context.Background())
	if eng.downloads != 0 || eng.restarts != 0 || gate.blocked != 0 {
		t.Fatalf("downloads=%d restarts=%d blocked=%d, want none", eng.downloads, eng.restarts, gate.blocked)
	}
	if len(emitted) == 0 || emitted[len(emitted)-1].Status != StatusAvailable {
		t.Errorf("last emitted state %+v", emitted)
	}
}

func TestMac_RestartRefusedWhileJobRuns(t *testing.T) {
	eng := &fakeEngine{rel: release("1.2.4")}
	jm := jobs.NewManager(func(core.Event) {})
	gate := &countingGate{block: jm.Block}
	m := NewManager(ManagerConfig{Mode: Install, Current: "1.2.3", Engine: eng, Gate: gate, CheckStaged: stagedOK})

	r := &blockingRunnable{started: make(chan struct{}), release: make(chan struct{})}
	if _, err := jm.Enqueue(context.Background(), &core.Plan{ID: "p", Runnable: r}); err != nil {
		t.Fatal(err)
	}
	<-r.started

	if s := m.Check(context.Background()); s.Status != StatusReady {
		t.Fatalf("state = %+v, want ready", s)
	}
	err := m.Restart(context.Background())
	var jr *ErrJobRunning
	if !errors.As(err, &jr) || !errors.Is(err, jobs.ErrJobActive) {
		t.Fatalf("Restart = %v, want ErrJobRunning", err)
	}
	if eng.restarts != 0 {
		t.Fatal("engine restarted during a job")
	}
	if s := m.State(); s.Status != StatusReady {
		t.Fatalf("state after refusal = %q, want ready", s.Status)
	}

	close(r.release)
	for jm.Busy() != nil {
		time.Sleep(time.Millisecond)
	}
	if err := m.Restart(context.Background()); err != nil {
		t.Fatal(err)
	}
	if eng.restarts != 1 || gate.blocked != 1 {
		t.Fatalf("restarts=%d blocked=%d", eng.restarts, gate.blocked)
	}
	// The app is on its way out; nothing may start a flash now.
	if _, err := jm.Enqueue(context.Background(), &core.Plan{ID: "q", Runnable: &blockingRunnable{}}); !errors.Is(err, jobs.ErrBlocked) {
		t.Fatalf("Enqueue after Restart = %v, want ErrBlocked", err)
	}
	if err := m.Restart(context.Background()); !errors.Is(err, ErrRestarting) {
		t.Fatalf("second Restart = %v, want ErrRestarting", err)
	}
}

type countingGate struct {
	block   func() (func(), error)
	blocked int
}

func (g *countingGate) BlockJobs() (func(), error) {
	rel, err := g.block()
	if err == nil {
		g.blocked++
	}
	return rel, err
}

type blockingRunnable struct {
	started chan struct{}
	release chan struct{}
}

func (r *blockingRunnable) StepInfos() []core.StepInfo { return nil }

func (r *blockingRunnable) Run(ctx context.Context, _ core.Executor) error {
	close(r.started)
	select {
	case <-r.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestMac_RestartFailureLetsJobsRunAgain(t *testing.T) {
	eng := &fakeEngine{rel: release("1.2.4"), restartErr: errors.New("helper did not start")}
	gate := &fakeGate{}
	m := NewManager(ManagerConfig{Mode: Install, Current: "1.2.3", Engine: eng, Gate: gate, CheckStaged: stagedOK})
	m.Check(context.Background())

	if err := m.Restart(context.Background()); err == nil {
		t.Fatal("Restart succeeded")
	}
	if gate.released != 1 {
		t.Fatal("jobs stay blocked after a failed restart")
	}
	if s := m.State(); s.Status != StatusError {
		t.Fatalf("state = %q, want error", s.Status)
	}
	// The next check downloads again rather than offering a staged bundle
	// that may be gone.
	if s := m.Check(context.Background()); s.Status != StatusReady || eng.downloads != 2 {
		t.Fatalf("state = %q after %d downloads, want ready after 2", s.Status, eng.downloads)
	}
}

func TestMac_UnwritableCopyOnlyNotifies(t *testing.T) {
	eng := &fakeEngine{rel: release("1.2.4")}
	gate := &fakeGate{}
	m := NewManager(ManagerConfig{Mode: Install, Current: "1.2.3", Engine: eng, Gate: gate, CheckStaged: stagedOK,
		CanInstall: func() error { return errors.New("running from the DMG") }})

	if s := m.Check(context.Background()); s.Status != StatusAvailable || s.ReleaseURL == "" {
		t.Fatalf("state = %+v, want available with a release URL", s)
	}
	if err := m.Restart(context.Background()); !errors.Is(err, ErrNotReady) {
		t.Fatalf("Restart = %v, want ErrNotReady", err)
	}
	if eng.downloads != 0 || eng.restarts != 0 {
		t.Fatal("downloaded or restarted a copy that cannot be replaced")
	}
}

func TestLinux_NoticeSurvivesFailedRecheck(t *testing.T) {
	eng := &fakeEngine{rel: release("1.2.4")}
	var emitted []State
	m := NewManager(ManagerConfig{Mode: Notify, Current: "1.2.3", Engine: eng, Gate: &fakeGate{},
		Emit: func(s State) { emitted = append(emitted, s) }})
	m.Check(context.Background())
	emitted = nil

	eng.checkErr = errors.New("offline")
	if s := m.Check(context.Background()); s.Status != StatusAvailable || s.Version != "1.2.4" {
		t.Fatalf("state = %+v, want the notice kept", s)
	}
	for _, s := range emitted {
		if s.Status != StatusAvailable {
			t.Fatalf("emitted %q during the re-check", s.Status)
		}
	}
}

func TestMac_BadStagedBundleIsNeverRestartedInto(t *testing.T) {
	eng := &fakeEngine{rel: release("1.2.4")}
	gate := &fakeGate{}
	m := NewManager(ManagerConfig{Mode: Install, Current: "1.2.3", Engine: eng, Gate: gate,
		CheckStaged: func(string, string) error { return errors.New("version mismatch") }})

	if s := m.Check(context.Background()); s.Status != StatusError {
		t.Fatalf("state = %+v, want error", s)
	}
	if err := m.Restart(context.Background()); !errors.Is(err, ErrNotReady) {
		t.Fatalf("Restart = %v, want ErrNotReady", err)
	}
	if eng.restarts != 0 || gate.blocked != 0 {
		t.Fatal("restarted into a rejected bundle")
	}
}

func TestMac_UpToDateAndErrors(t *testing.T) {
	eng := &fakeEngine{}
	m := NewManager(ManagerConfig{Mode: Install, Current: "1.2.3", Engine: eng, Gate: &fakeGate{}, CheckStaged: stagedOK})
	if s := m.Check(context.Background()); s.Status != StatusUpToDate {
		t.Fatalf("state = %q, want up-to-date", s.Status)
	}
	eng.checkErr = errors.New("offline")
	if s := m.Check(context.Background()); s.Status != StatusError || s.Error == "" {
		t.Fatalf("state = %+v, want error", s)
	}
	if eng.downloads != 0 {
		t.Fatal("downloaded without a release")
	}
}

func TestReady_StopsFurtherChecks(t *testing.T) {
	eng := &fakeEngine{rel: release("1.2.4")}
	m := NewManager(ManagerConfig{Mode: Install, Current: "1.2.3", Engine: eng, Gate: &fakeGate{}, CheckStaged: stagedOK})
	m.Check(context.Background())
	m.Check(context.Background())
	if eng.checks != 1 || eng.downloads != 1 {
		t.Fatalf("checks=%d downloads=%d, want 1 and 1", eng.checks, eng.downloads)
	}
}
