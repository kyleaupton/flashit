package service

// UpdaterService backs the "Check for updates" link.
type UpdaterService struct {
	version string
	check   func()
}

// NewUpdaterService takes nil for check when the updater is off.
func NewUpdaterService(version string, check func()) *UpdaterService {
	return &UpdaterService{version: version, check: check}
}

type UpdaterInfo struct {
	Enabled bool   `json:"enabled"`
	Version string `json:"version"`
}

func (s *UpdaterService) Info() UpdaterInfo {
	return UpdaterInfo{Enabled: s.check != nil, Version: s.version}
}

// CheckForUpdates opens the updater window, which stays up to say "up to
// date" when there is nothing newer.
func (s *UpdaterService) CheckForUpdates() {
	if s.check != nil {
		go s.check()
	}
}
