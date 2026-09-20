//go:build !linux && !darwin

package main

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

const defaultSocket = ""

func serve(context.Context, string, time.Duration, *slog.Logger) error {
	return errors.New("this host has no native helper yet")
}
