package steps

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

	wimPath, err := findInstallWim(state.ISOMountPath)
	if err != nil {
		return err
	}

	state.InstallWimPath = wimPath

	info, err := os.Stat(wimPath)
	if err != nil {
		return fmt.Errorf("failed to stat install.wim: %w", err)
	}

	state.NeedsSplit = info.Size() > fat32MaxFileSize

	e.Emit(core.Event{Type: "log", Message: fmt.Sprintf("install.wim size: %.2f GB (needs split: %v)",
		float64(info.Size())/1e9, state.NeedsSplit)})

	return nil
}

// findInstallWim locates the install.wim file in a mounted Windows ISO.
func findInstallWim(mountPoint string) (string, error) {
	candidates := []string{
		filepath.Join(mountPoint, "sources", "install.wim"),
		filepath.Join(mountPoint, "Sources", "install.wim"),
		filepath.Join(mountPoint, "SOURCES", "install.wim"),
	}

	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}

	return "", errors.New("install.wim not found in ISO")
}
