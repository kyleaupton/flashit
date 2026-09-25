//go:build linux || windows

package priv

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/kyleaupton/flashit/internal/logger"
	"github.com/kyleaupton/flashit/internal/proto"
)

// keepaliveInterval is a third of the helper's 60 s idle timeout, which the
// app does not override.
const keepaliveInterval = 20 * time.Second

// elevatedService runs the helper as root or admin for the whole job: one
// polkit or UAC prompt at EnsureReady, then a session kept alive by Hold.
type elevatedService struct {
	mu     sync.Mutex
	proc   *helperProcess
	client *Client
	hold   *keepalive
}

func platformService() PrivilegedService { return &elevatedService{} }

// EnsureReady reuses a live helper and otherwise spawns one, which raises the
// polkit or UAC prompt. The helper exits on idle, so a stale client is expected
// between jobs and simply replaced. A released helper that has not exited
// yet blocks a new spawn: the app cannot kill an elevated process, so it
// must not stack a second one.
func (s *elevatedService) EnsureReady(ctx context.Context) error {
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
		s.dropClient()
	}
	if s.proc != nil {
		if s.proc.alive() {
			return errors.New("the previous privileged helper is still shutting down; try again in a moment")
		}
		s.proc = nil
	}

	proc, err := spawnHelper(ctx)
	if proc != nil {
		s.proc = proc
	}
	if err != nil {
		return fmt.Errorf("start privileged helper: %w", err)
	}
	client := NewClient(proc.conn)
	info, err := client.Ping(ctx)
	if err != nil {
		client.Close()
		proc.release()
		return fmt.Errorf("helper handshake: %w", err)
	}
	logger.Info("privileged helper ready", "version", info.Version, "protocol", info.Protocol)
	s.proc = proc
	s.client = client
	return nil
}

func (s *elevatedService) Disk() DiskOps {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client == nil {
		return unavailableDiskOps{}
	}
	return &elevatedDiskOps{client: s.client}
}

// Hold pings the current session until release. Respawning would cost a
// second prompt, so a helper that is already gone stays gone and the
// next op reports it.
func (s *elevatedService) Hold() func() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopHold()
	if s.client == nil {
		return func() {}
	}
	k := startKeepalive(s.client, keepaliveInterval)
	s.hold = k
	return func() {
		k.stop()
		s.mu.Lock()
		if s.hold == k {
			s.hold = nil
		}
		s.mu.Unlock()
	}
}

func (s *elevatedService) stopHold() {
	if s.hold != nil {
		s.hold.stop()
		s.hold = nil
	}
}

func (s *elevatedService) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dropClient()
	return nil
}

// dropClient closes the session and releases the helper; proc stays set
// until it has actually exited.
func (s *elevatedService) dropClient() {
	s.stopHold()
	if s.client != nil {
		s.client.Close()
		s.client = nil
	}
	if s.proc != nil {
		s.proc.release()
	}
}

type elevatedDiskOps struct {
	client *Client
}

func (d *elevatedDiskOps) WriteISO(ctx context.Context, isoPath string, device string, progress ProgressFunc) error {
	image, err := os.Open(isoPath)
	if err != nil {
		return err
	}
	defer image.Close()
	fi, err := image.Stat()
	if err != nil {
		return err
	}
	return d.client.WriteImage(ctx, proto.WriteImageParams{Device: device, Size: fi.Size()}, image, progress)
}

func (d *elevatedDiskOps) FormatDisk(ctx context.Context, device string, filesystem string, volumeName string) error {
	_, err := d.client.FormatDisk(ctx, proto.FormatDiskParams{Device: device, Filesystem: filesystem, Label: volumeName})
	return err
}

func (d *elevatedDiskOps) Eject(ctx context.Context, device string) error {
	return d.client.Eject(ctx, device)
}

func (d *elevatedDiskOps) Unmount(ctx context.Context, device string) error {
	return d.client.Unmount(ctx, device)
}
