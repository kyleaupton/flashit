package steps

import "github.com/kyleaupton/flashit/internal/priv"

// FlashContext holds state shared between Linux flash pipeline steps.
type FlashContext struct {
	// Inputs (set before pipeline runs)
	ISOPath     string                 // Path to the source ISO file
	TargetDisk  string                 // Target device (e.g., /dev/disk4)
	PrivService priv.PrivilegedService // Privileged service for disk operations

	// Pipeline state (set during execution)
	TempISOPath string // Path to cloned ISO in neutral location (set by PrepareStep)
}
