//go:build darwin

package priv

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/kyleaupton/flashit/internal/logger"
	"github.com/kyleaupton/flashit/internal/proto"
)

// privacyPane is the System Settings pane where Removable Volumes is granted.
const privacyPane = "x-apple.systempreferences:com.apple.preference.security?Privacy_RemovableVolume"

type darwinService struct {
	mu         sync.Mutex
	proc       *helperProcess
	client     *Client
	findHelper func() (string, error)
}

func platformService() PrivilegedService { return &darwinService{findHelper: findHelper} }

// EnsureReady reuses a live helper and otherwise spawns one. Spawning costs
// no prompt: the helper runs as the user, and the sheet comes per op.
func (s *darwinService) EnsureReady(ctx context.Context) error {
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

	path, err := s.findHelper()
	if err != nil {
		return fmt.Errorf("start helper: %w", err)
	}
	proc, err := spawnHelper(path)
	if err != nil {
		return err
	}
	client := NewClient(proc.conn)
	info, err := client.Ping(ctx)
	if err != nil {
		client.Close()
		proc.release()
		return fmt.Errorf("helper handshake: %w", err)
	}
	logger.Info("helper ready", "version", info.Version, "protocol", info.Protocol, "pid", proc.cmd.Process.Pid)
	s.proc = proc
	s.client = client
	return nil
}

func (s *darwinService) Disk() DiskOps {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client == nil {
		return unavailableDiskOps{}
	}
	return &darwinDiskOps{client: s.client}
}

func (s *darwinService) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dropClient()
	return nil
}

func (s *darwinService) dropClient() {
	if s.client != nil {
		s.client.Close()
		s.client = nil
	}
	if s.proc != nil {
		s.proc.release()
		s.proc = nil
	}
}

type darwinDiskOps struct {
	client *Client
}

// WriteISO sends the open image and a fresh AuthorizationRef's external
// form; the helper raises the sheet on that ref right before the write. The
// ref is destroyed when the op returns, whatever happened.
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
	auth, err := newAuthorization()
	if err != nil {
		return err
	}
	defer auth.Free()
	return d.client.WriteImage(ctx, proto.WriteImageParams{Device: device, Size: fi.Size(), Authorization: auth.Token}, image, progress)
}

func (d *darwinDiskOps) FormatDisk(ctx context.Context, device string, filesystem string, volumeName string) error {
	auth, err := newAuthorization()
	if err != nil {
		return err
	}
	defer auth.Free()
	_, err = d.client.FormatDisk(ctx, proto.FormatDiskParams{Device: device, Filesystem: filesystem, Label: volumeName, Authorization: auth.Token})
	return err
}

func (d *darwinDiskOps) Eject(ctx context.Context, device string) error {
	return d.client.Eject(ctx, device)
}

// OpenPrivacySettings shows the Removable Volumes list under Privacy &
// Security, where a user who clicked Don't Allow can let FlashIt in.
func OpenPrivacySettings() error {
	return exec.Command("/usr/bin/open", privacyPane).Run()
}
