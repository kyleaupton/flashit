//go:build linux

package iso

import (
	"context"
	"errors"
)

var errMountNotImplemented = errors.New("iso: mount not implemented on linux")

type linuxMounter struct{}

func platformMounter() Mounter { return &linuxMounter{} }

func (m *linuxMounter) IsSupported() bool { return false }

func (m *linuxMounter) Mount(ctx context.Context, isoPath string) (*MountResult, error) {
	return nil, errMountNotImplemented
}

func (m *linuxMounter) Unmount(ctx context.Context, result *MountResult) error {
	return errMountNotImplemented
}

func (m *linuxMounter) DetachExisting(ctx context.Context, isoPath string) string {
	return ""
}
