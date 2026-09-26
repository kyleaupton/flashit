package main

import (
	"log/slog"
	"os"

	"github.com/kyleaupton/flashit/internal/logger"
)

// appLogger also writes to flashit.log in logger.Dir, which the ⋯ menu
// opens: a GUI build has no console to read.
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
