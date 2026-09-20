//go:build windows

package priv

import (
	"context"
	"sync"

	"github.com/kyleaupton/flashit/internal/drives"
	"github.com/kyleaupton/flashit/internal/logger"
	windowsclient "github.com/kyleaupton/flashit/internal/priv/windows"
)

type windowsService struct {
	client windowsclient.Client
	once   sync.Once
	ready  error
}

func platformService() PrivilegedService { return &windowsService{} }

func (s *windowsService) EnsureReady(ctx context.Context) error {
	s.once.Do(func() {
		logger.Debug("checking privileged helper status")

		// Initialize the Windows helper client
		c := windowsclient.NewClient()
		if c == nil {
			logger.Warn("Windows helper client not available")
			s.ready = nil
			return
		}

		if err := c.EnsureReady(ctx); err != nil {
			logger.Warn("Windows helper not ready", "error", err)
			s.ready = err
			return
		}

		logger.Debug("privileged helper ready via named pipe")
		s.client = c
		s.ready = nil
	})
	return s.ready
}

func (s *windowsService) Disk() DiskOps {
	if s.client != nil {
		return &windowsDiskOps{client: s.client}
	}
	return windowsDiskOpsFallback{}
}

func (s *windowsService) Shutdown(ctx context.Context) error {
	if s.client != nil {
		return s.client.Shutdown(ctx)
	}
	return nil
}

// windowsDiskOps uses the privileged helper for disk operations.
type windowsDiskOps struct {
	client windowsclient.Client
}

func (d *windowsDiskOps) WriteISO(ctx context.Context, isoPath string, device string, progress ProgressFunc) error {
	// Convert priv.ProgressFunc to windows.ProgressFunc
	var winProgress windowsclient.ProgressFunc
	if progress != nil {
		winProgress = func(written, total uint64) {
			progress(written, total)
		}
	}
	return d.client.WriteISO(ctx, isoPath, device, winProgress)
}

func (d *windowsDiskOps) FormatDisk(ctx context.Context, device string, filesystem string, volumeName string) error {
	return d.client.FormatDisk(ctx, device, filesystem, volumeName)
}

// Eject needs no elevation on Windows; the drives package does it as the user.
func (d *windowsDiskOps) Eject(ctx context.Context, device string) error {
	return drives.Eject(ctx, device)
}

// windowsDiskOpsFallback is used when the helper is not available.
// These operations will fail since raw disk access requires elevation.
type windowsDiskOpsFallback struct{}

func (d windowsDiskOpsFallback) WriteISO(ctx context.Context, isoPath string, device string, progress ProgressFunc) error {
	return windowsclient.ErrHelperNotRunning
}

func (d windowsDiskOpsFallback) FormatDisk(ctx context.Context, device string, filesystem string, volumeName string) error {
	return windowsclient.ErrHelperNotRunning
}

func (d windowsDiskOpsFallback) Eject(ctx context.Context, device string) error {
	return drives.Eject(ctx, device)
}
