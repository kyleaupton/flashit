//go:build linux

package priv

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/kyleaupton/flashit/internal/logger"
)

type linuxService struct {
	mu     sync.Mutex
	proc   *helperProcess
	client *Client
}

func platformService() PrivilegedService { return &linuxService{} }

// EnsureReady reuses a live helper and otherwise spawns one, which raises the
// polkit prompt. The helper exits on idle, so a stale client is expected
// between jobs and simply replaced.
func (s *linuxService) EnsureReady(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.client != nil {
		pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err := s.client.Ping(pingCtx)
		cancel()
		if err == nil {
			return nil
		}
		logger.Debug("helper gone, respawning", "error", err)
		s.teardown()
	}

	proc, err := spawnHelper(ctx)
	if err != nil {
		return fmt.Errorf("start privileged helper: %w", err)
	}
	client := NewClient(proc.conn)
	info, err := client.Ping(ctx)
	if err != nil {
		client.Close()
		proc.close()
		return fmt.Errorf("helper handshake: %w", err)
	}
	logger.Info("privileged helper ready", "version", info.Version, "protocol", info.Protocol)
	s.proc = proc
	s.client = client
	return nil
}

func (s *linuxService) Disk() DiskOps {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client == nil {
		return unavailableDiskOps{}
	}
	return &linuxDiskOps{client: s.client}
}

func (s *linuxService) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.teardown()
	return nil
}

func (s *linuxService) teardown() {
	if s.client != nil {
		s.client.Close()
		s.client = nil
	}
	if s.proc != nil {
		s.proc.close()
		s.proc = nil
	}
}

type linuxDiskOps struct {
	client *Client
}

func (d *linuxDiskOps) WriteISO(ctx context.Context, isoPath string, device string, progress ProgressFunc) error {
	abs, err := filepath.Abs(isoPath)
	if err != nil {
		return err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return err
	}
	return d.client.WriteImage(ctx, device, abs, fi.Size(), progress)
}

func (d *linuxDiskOps) FormatDisk(ctx context.Context, device string, filesystem string, volumeName string) error {
	_, err := d.client.FormatDisk(ctx, device, filesystem, volumeName)
	return err
}

func (d *linuxDiskOps) Eject(ctx context.Context, device string) error {
	return d.client.Eject(ctx, device)
}

type unavailableDiskOps struct{}

func (unavailableDiskOps) WriteISO(context.Context, string, string, ProgressFunc) error {
	return ErrHelperNotRunning
}

func (unavailableDiskOps) FormatDisk(context.Context, string, string, string) error {
	return ErrHelperNotRunning
}

func (unavailableDiskOps) Eject(context.Context, string) error { return ErrHelperNotRunning }
