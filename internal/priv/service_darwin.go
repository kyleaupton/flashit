//go:build darwin

package priv

import (
	"context"
	"errors"
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

func (s *darwinService) EnsureReady(ctx context.Context) error {
	_, err := s.session(ctx)
	return err
}

// session returns a live client, spawning a helper when there is none or the
// last one is gone. Every op goes through it, since a long unprivileged step
// can sit between EnsureReady and the op that needs the helper. Spawning
// costs no prompt: the helper runs as the user, and the sheet comes per op.
func (s *darwinService) session(ctx context.Context) (*Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.client != nil {
		pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err := s.client.Ping(pingCtx)
		cancel()
		if err == nil {
			return s.client, nil
		}
		logger.Debug("helper gone, respawning", "error", err)
		s.dropClient()
	}

	path, err := s.findHelper()
	if err != nil {
		return nil, fmt.Errorf("start helper: %w", err)
	}
	proc, err := spawnHelper(path)
	if err != nil {
		return nil, err
	}
	client := NewClient(proc.conn)
	info, err := client.Ping(ctx)
	if err != nil {
		client.Close()
		proc.release()
		return nil, fmt.Errorf("helper handshake: %w", err)
	}
	logger.Info("helper ready", "version", info.Version, "protocol", info.Protocol, "pid", proc.cmd.Process.Pid)
	s.proc = proc
	s.client = client
	return client, nil
}

// afterOp retires the helper behind a failed op when it cannot be trusted
// with the next one: TCC caches the Removable Volumes verdict per process,
// so a denial sticks until the user allows FlashIt and a fresh helper asks;
// and a helper whose connection the client gave up on (a cancel it never
// acknowledged, because a sheet was blocking it) is still holding that sheet.
func (s *darwinService) afterOp(c *Client, err error) {
	if err == nil {
		return
	}
	var pe *proto.Error
	denied := errors.As(err, &pe) && pe.Code == proto.CodeTCCDenied
	if !denied && !c.Broken() {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != c {
		return
	}
	logger.Info("retiring helper", "denied", denied, "broken", c.Broken())
	s.client = nil
	c.Close()
	if s.proc != nil {
		s.proc.kill()
		s.proc = nil
	}
}

func (s *darwinService) Disk() DiskOps { return &darwinDiskOps{s: s} }

// Hold is a no-op: the helper is an unprivileged child that session
// respawns without a prompt whenever an op needs it.
func (s *darwinService) Hold() func() { return func() {} }

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
	s *darwinService
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
	c, err := d.s.session(ctx)
	if err != nil {
		return err
	}
	auth, err := newAuthorization()
	if err != nil {
		return err
	}
	defer auth.Free()
	err = c.WriteImage(ctx, proto.WriteImageParams{Device: device, Size: fi.Size(), Authorization: auth.Token}, image, progress)
	d.s.afterOp(c, err)
	return err
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
	d.s.afterOp(c, err)
	return err
}

func (d *darwinDiskOps) Eject(ctx context.Context, device string) error {
	c, err := d.s.session(ctx)
	if err != nil {
		return err
	}
	err = c.Eject(ctx, device)
	d.s.afterOp(c, err)
	return err
}

func (d *darwinDiskOps) Unmount(ctx context.Context, device string) error {
	c, err := d.s.session(ctx)
	if err != nil {
		return err
	}
	err = c.Unmount(ctx, device)
	d.s.afterOp(c, err)
	return err
}

// OpenPrivacySettings shows the Removable Volumes list under Privacy &
// Security, where a user who clicked Don't Allow can let FlashIt in.
func OpenPrivacySettings() error {
	return exec.Command("/usr/bin/open", privacyPane).Run()
}
