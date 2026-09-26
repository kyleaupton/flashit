package service

import "github.com/kyleaupton/flashit/internal/logger"

// AppService backs the window's ⋯ menu.
type AppService struct {
	quit       func()
	about      func()
	openFolder func(path string) error
}

func NewAppService(quit, about func(), openFolder func(path string) error) *AppService {
	return &AppService{quit: quit, about: about, openFolder: openFolder}
}

// Quit goes through the same guard as the OS quit: while a job runs, the
// user is asked first.
func (s *AppService) Quit() { s.quit() }

func (s *AppService) ShowAbout() { s.about() }

func (s *AppService) OpenLogFolder() error {
	dir, err := logger.Dir()
	if err != nil {
		return err
	}
	return s.openFolder(dir)
}
