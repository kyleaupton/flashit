package main

import (
	"io"
	"log/slog"
	"os"

	"github.com/kyleaupton/flashit/internal/helper"
	"github.com/kyleaupton/flashit/internal/logger"
	"github.com/kyleaupton/flashit/internal/priv"
)

// runPrivilegedHelper serves as the privileged helper when the app started
// this copy of itself elevated for that. It must run before Wails starts.
func runPrivilegedHelper(args []string) (int, bool) {
	if len(args) == 0 || args[0] != priv.HelperFlag {
		return 0, false
	}
	return helper.ServeWindows(args[1:], Version), true
}

// appLogger also writes to %LOCALAPPDATA%\FlashIt\logs\flashit.log: a GUI
// build has no console to read.
func appLogger() *slog.Logger {
	f, err := logger.OpenFile("flashit.log")
	if err != nil {
		return nil
	}
	return slog.New(slog.NewTextHandler(io.MultiWriter(os.Stderr, f), &slog.HandlerOptions{Level: slog.LevelInfo}))
}
