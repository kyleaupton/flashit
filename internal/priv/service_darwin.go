//go:build darwin

package priv

import (
	"context"
	"os"
	"sync"

	"github.com/kyleaupton/flashit/internal/logger"
	"github.com/kyleaupton/flashit/internal/proto"
)

type darwinService struct {
	// mu guards client and cancel; it is never held while connecting, so
	// Shutdown can always get it and cut a connect attempt short.
	mu     sync.Mutex
	client *Client
	cancel context.CancelFunc
	// connecting serializes connect attempts so two ops do not race to
	// register the daemon.
	connecting sync.Mutex
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
	s.connecting.Lock()
	defer s.connecting.Unlock()

	s.mu.Lock()
	current := s.client
	s.mu.Unlock()
	if current != nil {
		pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
		_, err := current.Ping(pingCtx)
		cancel()
		if err == nil {
			return current, nil
		}
		logger.Debug("helper session gone, reconnecting", "error", err)
		s.drop(current)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.mu.Lock()
	s.cancel = cancel
	s.mu.Unlock()
	c, err := connectHelper(ctx)
	s.mu.Lock()
	s.cancel = nil
	if err == nil {
		s.client = c
	}
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	logger.Info("privileged helper ready", "version", HelperVersion)
	return c, nil
}

func (s *darwinService) drop(c *Client) {
	s.mu.Lock()
	if s.client == c {
		s.client = nil
	}
	s.mu.Unlock()
	c.Close()
}

func (s *darwinService) Disk() DiskOps { return &darwinDiskOps{s: s} }

func (s *darwinService) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	c := s.client
	s.client = nil
	s.mu.Unlock()
	if c != nil {
		c.Close()
	}
	return nil
}

type darwinDiskOps struct {
	s *darwinService
}

func (d *darwinDiskOps) WriteISO(ctx context.Context, isoPath string, device string, progress ProgressFunc) error {
	image, err := os.Open(isoPath)
	if err != nil {
		return err
	}
	defer image.Close()
	fi, err := image.Stat()
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
	return c.WriteImage(ctx, proto.WriteImageParams{Device: device, Size: fi.Size(), Authorization: auth.Token}, image, progress)
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
