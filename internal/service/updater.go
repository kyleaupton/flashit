package service

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/updater"

	"github.com/kyleaupton/flashit/internal/eventbus"
	"github.com/kyleaupton/flashit/internal/logger"
)

// UpdaterService drives the Wails updater headless; the frontend draws every
// state from its wails:updater:* events.
type UpdaterService struct {
	version string
	up      *updater.Updater // nil when the updater is off
	busy    func() bool

	mu      sync.Mutex
	pending *Release
}

// NewUpdaterService takes nil for up when the updater is off. busy reports
// whether a job is running, which Restart refuses.
func NewUpdaterService(version string, up *updater.Updater, busy func() bool) *UpdaterService {
	return &UpdaterService{version: version, up: up, busy: busy}
}

// Release is what the update sheet shows of a newer version. Notes is the
// GitHub release body, Markdown.
type Release struct {
	Version     string    `json:"version"`
	Name        string    `json:"name"`
	Notes       string    `json:"notes"`
	PublishedAt time.Time `json:"publishedAt"`
}

type UpdaterInfo struct {
	Enabled bool   `json:"enabled"`
	Version string `json:"version"`
	// State is the updater's phase ("idle", "available", "downloading",
	// "ready", ...) so a page loaded mid-flow can catch up.
	State   string   `json:"state"`
	Pending *Release `json:"pending"`
}

func (s *UpdaterService) Info() UpdaterInfo {
	info := UpdaterInfo{Enabled: s.up != nil, Version: s.version}
	if s.up == nil {
		return info
	}
	info.State = string(s.up.State())
	s.mu.Lock()
	info.Pending = s.pending
	s.mu.Unlock()
	return info
}

var (
	ErrUpdaterOff  = errors.New("updates are not available for this build")
	ErrRestartBusy = errors.New("FlashIt can restart once the drive is finished")
)

// Check asks the release feed for a newer version. It returns nil when
// this one is the latest.
func (s *UpdaterService) Check(ctx context.Context) (*Release, error) {
	if s.up == nil {
		return nil, ErrUpdaterOff
	}
	rel, err := s.up.Check(ctx)
	if err != nil {
		return nil, err
	}
	var pending *Release
	if rel != nil {
		pending = &Release{Version: rel.Version, Name: rel.Name, Notes: rel.Notes, PublishedAt: rel.PublishedAt}
	}
	s.mu.Lock()
	s.pending = pending
	s.mu.Unlock()
	return pending, nil
}

// Install downloads, verifies and stages the release the last Check found.
// It returns at once; progress and the outcome arrive as updater events.
func (s *UpdaterService) Install() error {
	if s.up == nil {
		return ErrUpdaterOff
	}
	go func() {
		err := s.up.DownloadAndInstall(context.Background())
		if err == nil {
			return
		}
		logger.Error("update install failed", "error", err)
		// Refusals before the download (one already running, nothing
		// pending) emit no event of their own, and the sheet waits on one.
		if s.up.State() != updater.StateError {
			eventbus.Emit(updater.EventError, updater.ErrorInfo{Stage: updater.StageDownload, Message: err.Error()})
		}
	}()
	return nil
}

// Restart relaunches into the staged update. It refuses while a job is
// running, since quitting would leave the drive half written.
func (s *UpdaterService) Restart(ctx context.Context) error {
	if s.up == nil {
		return ErrUpdaterOff
	}
	if s.busy() {
		return ErrRestartBusy
	}
	return s.up.Restart(ctx)
}
