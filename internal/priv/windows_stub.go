//go:build windows

package priv

import (
	"context"
	"errors"
)

type windowsRunner struct{}

func platformRunner() Runner { return &windowsRunner{} }

func (w *windowsRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	return "", errors.New("privileged runner not implemented on windows yet")
}
