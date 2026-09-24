package steps

import (
	"github.com/kyleaupton/flashit/internal/iso"
	"github.com/kyleaupton/flashit/internal/priv"
)

// FlashContext holds state shared between Windows flash pipeline steps.
type FlashContext struct {
	// Inputs (set before pipeline runs)
	ISOPath     string                 // Path to the source Windows ISO file
	TargetDisk  string                 // Target device (e.g., /dev/disk4)
	VolumeName  string                 // Volume name for the USB drive
	PrivService priv.PrivilegedService // Privileged service for disk operations

	// Pipeline state (set during execution)
	ISOMountResult *iso.MountResult // ISO mount result (set by MountISO)
	ISOMountPath   string           // Path where ISO is mounted (set by MountISO)
	USBMountPath   string           // Path where USB is mounted (set by FormatUSB)
	InstallWimPath string           // Path to install.wim (set by AnalyzeWim)
	NeedsSplit     bool             // Whether install.wim exceeds FAT32 limit (set by AnalyzeWim)
}
