//go:build !windows

package main

import "log/slog"

// The privileged helper is its own executable here (cmd/flashit-helper).
func runPrivilegedHelper([]string) (int, bool) { return 0, false }

// nil keeps Wails' default logger.
func appLogger() *slog.Logger { return nil }
