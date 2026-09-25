// Package update drives the Wails updater (pkg/updater) for FlashIt: a full
// update on macOS, a notice on Linux, nothing on Windows or in dev builds.
//
// The Wails updater relaunches the app in its own helper mode to swap the
// bundle. That is unrelated to cmd/flashit-helper, the privileged helper.
package update

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/updater"
	"github.com/wailsapp/wails/v3/pkg/updater/providers/endpoint"

	"github.com/kyleaupton/flashit/internal/logger"
)

// Mode is what the updater does on this build.
type Mode string

const (
	Off     Mode = "off"
	Notify  Mode = "notify"
	Install Mode = "install"
)

// releaseVersion is what a tag release builds with. Dev builds, git
// describe versions and the 0.0.0-dev.N of manual package runs never check.
var releaseVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// ModeFor picks the mode for a platform and build version. Windows waits
// for v1.1: the Wails swap replaces one file, and the Windows zip holds the
// app and the old helper.
func ModeFor(goos, version string) Mode {
	if !releaseVersion.MatchString(version) {
		return Off
	}
	switch goos {
	case "darwin":
		return Install
	case "linux":
		// The app lives in root-owned /usr/bin and the package manager owns it.
		return Notify
	}
	return Off
}

type Status string

const (
	StatusIdle        Status = "idle"
	StatusChecking    Status = "checking"
	StatusUpToDate    Status = "up-to-date"
	StatusAvailable   Status = "available"
	StatusDownloading Status = "downloading"
	StatusReady       Status = "ready"
	StatusRestarting  Status = "restarting"
	StatusError       Status = "error"
)

// State is what the frontend renders. Version and Notes come from the
// unsigned manifest; Version is checked to be a plain release version, and
// ReleaseURL is built from it here rather than taken from the manifest.
type State struct {
	Mode       Mode   `json:"mode"`
	Status     Status `json:"status"`
	Current    string `json:"current"`
	Version    string `json:"version,omitempty"`
	Notes      string `json:"notes,omitempty"`
	ReleaseURL string `json:"releaseUrl,omitempty"`
	Error      string `json:"error,omitempty"`
}

// Engine is the part of *updater.Updater the manager uses.
type Engine interface {
	Check(ctx context.Context) (*updater.Release, error)
	DownloadAndInstall(ctx context.Context) error
	Restart(ctx context.Context) error
	DownloadedPath() string
}

// JobGate keeps flashes from starting while the app restarts.
type JobGate interface {
	BlockJobs() (release func(), err error)
}

var (
	ErrOff        = errors.New("updates are off in this build")
	ErrNotReady   = errors.New("no update is ready to install")
	ErrRestarting = errors.New("already restarting")
)

// ErrJobRunning wraps the gate's refusal.
type ErrJobRunning struct{ Err error }

func (e *ErrJobRunning) Error() string {
	return "cannot restart while a flash is in progress: " + e.Err.Error()
}
func (e *ErrJobRunning) Unwrap() error { return e.Err }

const repo = "https://github.com/kyleaupton/flashit"

// ManifestURL is where release builds look. latest skips prereleases, so
// there is one channel.
const ManifestURL = repo + "/releases/latest/download/manifest.json"

// ReleaseArtifactURL is where the release workflow publishes filename.
func ReleaseArtifactURL(version, filename string) string {
	return repo + "/releases/download/v" + version + "/" + filename
}

func releasePage(version string) string {
	return repo + "/releases/tag/v" + version
}

const maxNotes = 4000

type Manager struct {
	mode        Mode
	engine      Engine
	gate        JobGate
	emit        func(State)
	checkStaged func(path, version string) error

	mu    sync.Mutex
	state State
	busy  bool
}

type ManagerConfig struct {
	Mode    Mode
	Current string
	Engine  Engine
	Gate    JobGate
	Emit    func(State)
	// CheckStaged vets the unpacked update before it can be restarted
	// into. Required in Install mode.
	CheckStaged func(path, version string) error
}

func NewManager(cfg ManagerConfig) *Manager {
	emit := cfg.Emit
	if emit == nil {
		emit = func(State) {}
	}
	return &Manager{
		mode:        cfg.Mode,
		engine:      cfg.Engine,
		gate:        cfg.Gate,
		emit:        emit,
		checkStaged: cfg.CheckStaged,
		state:       State{Mode: cfg.Mode, Status: StatusIdle, Current: cfg.Current},
	}
}

// Config is what New needs besides the app's *updater.Updater.
type Config struct {
	GOOS        string
	Version     string
	ManifestURL string
	ArtifactURL func(version, filename string) string
	PublicKey   []byte
	Gate        JobGate
	Emit        func(State)
}

// New sets up u for this build, or leaves it unconfigured and returns an Off
// manager. It never sets CheckInterval: the Wails poll loop runs
// CheckAndInstall, which downloads on every platform and subscribes to
// restart events any page can emit. Run polls instead.
func New(u *updater.Updater, cfg Config) (*Manager, error) {
	return setup(u, cfg, CheckBundle)
}

func setup(u *updater.Updater, cfg Config, checkStaged func(path, version string) error) (*Manager, error) {
	mode := ModeFor(cfg.GOOS, cfg.Version)
	mc := ManagerConfig{Mode: mode, Current: cfg.Version, Engine: u, Gate: cfg.Gate, Emit: cfg.Emit, CheckStaged: checkStaged}
	if mode == Off {
		return NewManager(mc), nil
	}
	if len(cfg.PublicKey) == 0 {
		return nil, errors.New("update: no public key")
	}
	ep, err := endpoint.New(endpoint.Config{
		URL: cfg.ManifestURL,
		// The default 30 s covers the whole archive download too.
		HTTPClient: &http.Client{Timeout: 15 * time.Minute},
	})
	if err != nil {
		return nil, err
	}
	err = u.Init(updater.Config{
		CurrentVersion: cfg.Version,
		Providers:      []updater.Provider{&guard{next: ep, goos: cfg.GOOS, artifactURL: cfg.ArtifactURL}},
		PublicKey:      cfg.PublicKey,
		Platform:       cfg.GOOS,
		Window:         updater.WindowNone,
	})
	if err != nil {
		return nil, err
	}
	return NewManager(mc), nil
}

func (m *Manager) Mode() Mode { return m.mode }

func (m *Manager) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

func (m *Manager) set(f func(*State)) State {
	m.mu.Lock()
	f(&m.state)
	s := m.state
	m.mu.Unlock()
	m.emit(s)
	return s
}

// Run checks once after first, then every interval, until ctx ends.
func (m *Manager) Run(ctx context.Context, first, interval time.Duration) {
	if m.mode == Off {
		return
	}
	t := time.NewTimer(first)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		m.Check(ctx)
		t.Reset(interval)
	}
}

// Check looks for an update and, on macOS, downloads, verifies and unpacks
// it. It returns at once with the current state while another check runs
// or an update is already ready.
func (m *Manager) Check(ctx context.Context) State {
	m.mu.Lock()
	if m.mode == Off || m.busy || m.state.Status == StatusReady || m.state.Status == StatusRestarting {
		s := m.state
		m.mu.Unlock()
		return s
	}
	m.busy = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.busy = false
		m.mu.Unlock()
	}()

	m.set(func(s *State) { s.Status, s.Error = StatusChecking, "" })
	rel, err := m.engine.Check(ctx)
	if err != nil {
		return m.fail("check", err)
	}
	if rel == nil {
		return m.set(func(s *State) {
			*s = State{Mode: s.Mode, Status: StatusUpToDate, Current: s.Current}
		})
	}
	found := func(status Status) func(*State) {
		return func(s *State) {
			*s = State{
				Mode:       s.Mode,
				Status:     status,
				Current:    s.Current,
				Version:    rel.Version,
				Notes:      truncate(rel.Notes, maxNotes),
				ReleaseURL: releasePage(rel.Version),
			}
		}
	}
	if m.mode == Notify {
		return m.set(found(StatusAvailable))
	}

	m.set(found(StatusDownloading))
	if err := m.engine.DownloadAndInstall(ctx); err != nil {
		return m.fail("download", err)
	}
	staged := m.engine.DownloadedPath()
	if err := m.checkStaged(staged, rel.Version); err != nil {
		discard(staged)
		return m.fail("staged bundle", err)
	}
	return m.set(found(StatusReady))
}

func (m *Manager) fail(stage string, err error) State {
	logger.Warn("update "+stage+" failed", "error", err)
	return m.set(func(s *State) {
		*s = State{Mode: s.Mode, Status: StatusError, Current: s.Current, Error: err.Error()}
	})
}

// Restart swaps in the ready update and relaunches. It refuses while a
// flash is pending, running or being planned, and holds new flashes off
// from then on; if the restart fails they are allowed again.
func (m *Manager) Restart(ctx context.Context) error {
	m.mu.Lock()
	switch {
	case m.mode != Install:
		m.mu.Unlock()
		return ErrOff
	case m.state.Status == StatusRestarting:
		m.mu.Unlock()
		return ErrRestarting
	case m.state.Status != StatusReady:
		m.mu.Unlock()
		return ErrNotReady
	}
	release, err := m.gate.BlockJobs()
	if err != nil {
		m.mu.Unlock()
		return &ErrJobRunning{Err: err}
	}
	m.state.Status = StatusRestarting
	s := m.state
	m.mu.Unlock()
	m.emit(s)

	if err := m.engine.Restart(ctx); err != nil {
		release()
		m.set(func(s *State) { s.Status = StatusReady })
		return fmt.Errorf("restart: %w", err)
	}
	return nil
}

// discard removes the staging directory the Wails updater made for path.
// The prefix check keeps a surprising path from taking anything else.
func discard(path string) {
	if path == "" {
		return
	}
	dir := filepath.Dir(path)
	if strings.HasPrefix(filepath.Base(dir), "wails-update-") {
		_ = os.RemoveAll(dir)
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
