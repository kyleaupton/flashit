//go:build darwin

package priv

import (
	"context"
	"os"
	"path/filepath"
	"sync"

	"github.com/kyleaupton/flashit/internal/logger"
	"github.com/kyleaupton/flashit/internal/proto"
)

type darwinService struct {
	mu     sync.Mutex
	client *Client
}

func platformService() PrivilegedService { return &darwinService{} }

func (s *darwinService) EnsureReady(ctx context.Context) error {
	_, err := s.session(ctx)
	return err
}

// session returns the live client, reconnecting when the daemon dropped the
// previous one (it ends a session that goes quiet between ops). Reconnecting
// costs no prompt: caller authentication is silent, and user authorization
// is per op.
func (s *darwinService) session(ctx context.Context) (*Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil {
		pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
		_, err := s.client.Ping(pingCtx)
		cancel()
		if err == nil {
			return s.client, nil
		}
		logger.Debug("helper session gone, reconnecting", "error", err)
		s.client.Close()
		s.client = nil
	}
	c, err := connectHelper(ctx)
	if err != nil {
		return nil, err
	}
	logger.Info("privileged helper ready", "version", expectedHelperVersion())
	s.client = c
	return c, nil
}

func (s *darwinService) Disk() DiskOps { return &darwinDiskOps{s: s} }

func (s *darwinService) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil {
		s.client.Close()
		s.client = nil
	}
	return nil
}

type darwinDiskOps struct {
	s *darwinService
}

func (d *darwinDiskOps) WriteISO(ctx context.Context, isoPath string, device string, progress ProgressFunc) error {
	abs, err := filepath.Abs(isoPath)
	if err != nil {
		return err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return err
	}
	c, err := d.s.session(ctx)
	if err != nil {
		return err
	}
	auth, err := newAuthorization()
	if err != nil {
		return err
	}
	defer auth.Free()
	return c.WriteImage(ctx, proto.WriteImageParams{Device: device, Source: abs, Size: fi.Size(), Authorization: auth.Token}, progress)
}

func (d *darwinDiskOps) FormatDisk(ctx context.Context, device string, filesystem string, volumeName string) error {
	c, err := d.s.session(ctx)
	if err != nil {
		return err
	}
	auth, err := newAuthorization()
	if err != nil {
		return err
	}
	defer auth.Free()
	_, err = c.FormatDisk(ctx, proto.FormatDiskParams{Device: device, Filesystem: filesystem, Label: volumeName, Authorization: auth.Token})
	return err
}

func (d *darwinDiskOps) Eject(ctx context.Context, device string) error {
	c, err := d.s.session(ctx)
	if err != nil {
		return err
	}
	return c.Eject(ctx, device)
}

// HelperStatus reports the daemon's registration as the frontend needs it:
// ready, needs-approval or not-installed.
func HelperStatus() (string, error) {
	switch smDaemonStatus() {
	case smEnabled:
		return "ready", nil
	case smRequiresApproval:
		return "needs-approval", nil
	default:
		return "not-installed", nil
	}
}

// OpenHelperSettings shows System Settings > Login Items & Extensions, where
// the user approves the daemon.
func OpenHelperSettings() error {
	smOpenSettings()
	return nil
}
