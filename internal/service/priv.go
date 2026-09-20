package service

import "github.com/kyleaupton/flashit/internal/priv"

// PrivService exposes what the frontend needs from the privileged helper.
type PrivService struct{}

func NewPrivService() *PrivService { return &PrivService{} }

// OpenPrivacySettings opens the system pane where the user grants FlashIt
// access to removable volumes. It is a no-op on hosts without one.
func (s *PrivService) OpenPrivacySettings() error {
	return priv.OpenPrivacySettings()
}
