//go:build !darwin

package iso

import (
	"context"
	"errors"
)

// Linux and Windows read Windows ISOs in process through isofs.
var errMountNotImplemented = errors.New("iso: mounting is not used on this host")

type otherMounter struct{}

func platformMounter() Mounter { return &otherMounter{} }

func (m *otherMounter) IsSupported() bool { return false }

func (m *otherMounter) Mount(ctx context.Context, isoPath string) (*MountResult, error) {
	return nil, errMountNotImplemented
}

func (m *otherMounter) Unmount(ctx context.Context, result *MountResult) error {
	return errMountNotImplemented
}

func (m *otherMounter) DetachExisting(ctx context.Context, isoPath string) string {
	return ""
}
