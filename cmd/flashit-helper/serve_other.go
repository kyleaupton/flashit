//go:build !linux && !darwin

package main

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

func serve(context.Context, string, time.Duration, *slog.Logger) error {
	return errors.New("no standalone helper here; on Windows flashit.exe --privileged-helper is the helper")
}
