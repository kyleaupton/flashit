package service

import (
	"context"
	"errors"

	"github.com/kyleaupton/flashit/internal/update"
)

// UpdateService is the frontend's view of the updater.
type UpdateService struct {
	mgr     *update.Manager
	openURL func(string) error
	ctx     context.Context
}

// ctx is the app's, so a check or download ends when FlashIt quits rather
// than when the frontend's call does.
func NewUpdateService(ctx context.Context, mgr *update.Manager, openURL func(string) error) *UpdateService {
	return &UpdateService{mgr: mgr, openURL: openURL, ctx: ctx}
}

func (s *UpdateService) GetState() update.State {
	return s.mgr.State()
}

// CheckNow checks at once and returns when the check, and on macOS the
// download, is done.
func (s *UpdateService) CheckNow() update.State {
	return s.mgr.Check(s.ctx)
}

// Restart installs the ready update and relaunches FlashIt. It is refused
// while a flash is pending or running.
func (s *UpdateService) Restart() error {
	return s.mgr.Restart(s.ctx)
}

// OpenReleasePage opens the page of the version found, built from that
// version rather than taken from the frontend or the manifest.
func (s *UpdateService) OpenReleasePage() error {
	url := s.mgr.State().ReleaseURL
	if url == "" {
		return errors.New("no update has been found")
	}
	return s.openURL(url)
}
