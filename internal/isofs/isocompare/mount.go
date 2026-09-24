//go:build !linux

package isocompare

import (
	"context"

	"github.com/kyleaupton/flashit/internal/iso"
)

// Mount mounts the image read-only through the host and returns where.
func Mount(ctx context.Context, path string) (string, func(), error) {
	res, err := iso.Mount(ctx, path)
	if err != nil {
		return "", nil, err
	}
	return res.MountPath, func() { iso.Unmount(context.Background(), res) }, nil
}
