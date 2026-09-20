//go:build linux

package priv

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

type linuxRunner struct{}

func platformRunner() Runner { return &linuxRunner{} }

func (l *linuxRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "pkexec", append([]string{name}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("pkexec %s failed: %w (%s)", name, err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}
