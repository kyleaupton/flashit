package main

import (
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
	return slog.New(slog.NewTextHandler(teeFile{f}, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

// teeFile writes to the file and, best effort, to stderr, which in a
// windowsgui build is handle 0 and fails every write; io.MultiWriter would
// stop there and never reach the file.
type teeFile struct{ f *os.File }

func (t teeFile) Write(p []byte) (int, error) {
	_, _ = os.Stderr.Write(p)
	return t.f.Write(p)
}
