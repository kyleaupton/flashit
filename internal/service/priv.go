package service

import "github.com/kyleaupton/flashit/internal/priv"

// PrivService exposes the privileged helper's install state to the frontend.
type PrivService struct{}

func NewPrivService() *PrivService { return &PrivService{} }

// HelperStatus is "ready", "needs-approval" or "not-installed".
func (s *PrivService) HelperStatus() (string, error) {
	return priv.HelperStatus()
}

// OpenHelperSettings opens the system pane where the user approves the
// helper. It is a no-op on hosts without one.
func (s *PrivService) OpenHelperSettings() error {
	return priv.OpenHelperSettings()
}
