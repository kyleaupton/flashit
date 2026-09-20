//go:build darwin

package priv

import (
	"context"
	"errors"
	"sync"

	"github.com/kyleaupton/flashit/internal/drives"
	"github.com/kyleaupton/flashit/internal/logger"
	macosclient "github.com/kyleaupton/flashit/internal/priv/macos"
)

type darwinService struct {
	client macosclient.Client
	once   sync.Once
	ready  error
}

func platformService() PrivilegedService { return &darwinService{} }

func (s *darwinService) EnsureReady(ctx context.Context) error {
	s.once.Do(func() {
		logger.Debug("checking privileged helper status")
		// Try to initialize XPC helper first.
		c := macosclient.NewClient()
		if c != nil {
			if err := c.EnsureReady(ctx); err == nil {
				logger.Debug("privileged helper ready via XPC")
				s.client = c
				s.ready = nil
				return
			} else {
				logger.Warn("XPC helper not ready, falling back", "error", err)
			}
		}
		// Fallback: no helper yet; direct execution will be used.
		logger.Debug("using fallback privileged execution")
		s.ready = nil
	})
	return s.ready
}

func (s *darwinService) Disk() DiskOps {
	if s.client != nil {
		return &darwinDiskOpsXPC{client: s.client}
	}
	return darwinDiskOps{}
}

func (s *darwinService) Shutdown(ctx context.Context) error { return nil }

// darwinDiskOps is the fallback implementation without XPC helper.
// These operations require running as root or via sudo.
type darwinDiskOps struct{}

func (d darwinDiskOps) WriteISO(ctx context.Context, isoPath string, device string, progress ProgressFunc) error {
	// Without the XPC helper, we can't write ISOs on modern macOS
	// due to raw disk access restrictions.
	return errors.New("WriteISO requires the privileged helper to be installed")
}

func (d darwinDiskOps) FormatDisk(ctx context.Context, device string, filesystem string, volumeName string) error {
	// Without the XPC helper, we can't format disks on modern macOS
	// due to disk access restrictions.
	return errors.New("FormatDisk requires the privileged helper to be installed")
}

// Eject needs no privilege on macOS; diskutil does it as the user.
func (d darwinDiskOps) Eject(ctx context.Context, device string) error {
	return drives.Eject(ctx, device)
}

// darwinDiskOpsXPC uses the privileged XPC helper for disk operations.
type darwinDiskOpsXPC struct{ client macosclient.Client }

func (d *darwinDiskOpsXPC) WriteISO(ctx context.Context, isoPath string, device string, progress ProgressFunc) error {
	// Convert priv.ProgressFunc to macos.ProgressFunc
	var macosProgress macosclient.ProgressFunc
	if progress != nil {
		macosProgress = func(written, total uint64) {
			progress(written, total)
		}
	}
	return d.client.WriteLinuxISO(ctx, isoPath, device, macosProgress)
}

func (d *darwinDiskOpsXPC) FormatDisk(ctx context.Context, device string, filesystem string, volumeName string) error {
	return d.client.FormatDisk(ctx, device, filesystem, volumeName)
}

func (d *darwinDiskOpsXPC) Eject(ctx context.Context, device string) error {
	return drives.Eject(ctx, device)
}
