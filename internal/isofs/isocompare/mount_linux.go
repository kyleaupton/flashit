package isocompare

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
)

// Mount loop-mounts the image read-only and returns where. It needs root.
func Mount(ctx context.Context, path string) (string, func(), error) {
	if os.Geteuid() != 0 {
		return "", nil, errors.New("loop-mounting an ISO needs root on Linux")
	}
	dir, err := os.MkdirTemp("", "isocompare-")
	if err != nil {
		return "", nil, err
	}
	if out, err := exec.CommandContext(ctx, "mount", "-o", "loop,ro", path, dir).CombinedOutput(); err != nil {
		os.Remove(dir)
		return "", nil, fmt.Errorf("mount: %s: %w", out, err)
	}
	return dir, func() {
		exec.Command("umount", dir).Run()
		os.Remove(dir)
	}, nil
}
