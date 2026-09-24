package steps

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"time"

	"github.com/kyleaupton/flashit/internal/core"
	"github.com/kyleaupton/flashit/internal/pipeline"
)

const (
	// FAT32 max file size is 4GB - 1 byte
	fat32MaxFileSize = 4*1024*1024*1024 - 1
)

// AnalyzeWim finds install.wim and checks if it needs splitting for FAT32.
type AnalyzeWim struct{}

func (AnalyzeWim) Key() string       { return "analyzing-wim" }
func (AnalyzeWim) Name() string      { return "Analyzing install.wim" }
func (AnalyzeWim) HasProgress() bool { return false }

func (AnalyzeWim) Run(ctx context.Context, state *FlashContext, e core.Executor) error {
	if core.DryRun {
		// Simulate WIM needs splitting in dry-run mode
		state.NeedsSplit = true
		return pipeline.Simulate(ctx, e, 500*time.Millisecond, 3)
	}

	e.Emit(core.Event{Type: "log", Message: "Analyzing install.wim..."})

	wimPath, err := findInstallWim(state.Source)
	if err != nil {
		return err
	}
	info, err := fs.Stat(state.Source, wimPath)
	if err != nil {
		return fmt.Errorf("failed to stat install.wim: %w", err)
	}

	state.InstallWim = wimPath
	state.InstallWimSize = info.Size()
	state.NeedsSplit = info.Size() > fat32MaxFileSize

	e.Emit(core.Event{Type: "log", Message: fmt.Sprintf("install.wim size: %.2f GB (needs split: %v)",
		float64(info.Size())/1e9, state.NeedsSplit)})

	return nil
}

// findInstallWim locates sources/install.wim in any letter case.
func findInstallWim(src fs.FS) (string, error) {
	dir, err := findEntry(src, ".", "sources", true)
	if err != nil {
		return "", err
	}
	return findEntry(src, dir, "install.wim", false)
}

func findEntry(src fs.FS, dir, name string, wantDir bool) (string, error) {
	entries, err := fs.ReadDir(src, dir)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if strings.EqualFold(e.Name(), name) && e.IsDir() == wantDir {
			return path.Join(dir, e.Name()), nil
		}
	}
	return "", errors.New("install.wim not found in ISO")
}
