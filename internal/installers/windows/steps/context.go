package steps

import (
	"context"
	"io/fs"

	"github.com/kyleaupton/flashit/internal/core"
	"github.com/kyleaupton/flashit/internal/priv"
)

// FlashContext holds state shared between Windows flash pipeline steps.
type FlashContext struct {
	// Inputs (set before pipeline runs)
	ISOPath     string                 // Path to the source Windows ISO file
	TargetDisk  string                 // Target device (e.g., /dev/disk4)
	VolumeName  string                 // Volume name for the USB drive
	PrivService priv.PrivilegedService // Privileged service for disk operations
	// HelperMounts is set where the helper's format mounts the volume as
	// root (Linux): only the helper can unmount it then.
	HelperMounts bool
	// HelperEjects is set where the helper ejects the stick (Linux,
	// Windows), which reports a volume still in use as device_busy.
	HelperEjects bool

	// Pipeline state (set during execution)
	Source         fs.FS  // ISO contents, read in process or through a mount (set by OpenSource)
	USBMountPath   string // Path where USB is mounted (set by FormatUSB)
	InstallWim     string // install.wim's path within Source (set by AnalyzeWim)
	InstallWimSize int64  // (set by AnalyzeWim)
	NeedsSplit     bool   // Whether install.wim exceeds FAT32 limit (set by AnalyzeWim)

	closeSource func(context.Context)
}

// releaseSource closes or unmounts whatever OpenSource opened.
func (s *FlashContext) releaseSource(ctx context.Context, e core.Executor) {
	if s.closeSource == nil {
		return
	}
	e.Emit(core.Event{Type: core.EventLog, Message: "Closing ISO..."})
	s.closeSource(ctx)
	s.closeSource = nil
	s.Source = nil
}
