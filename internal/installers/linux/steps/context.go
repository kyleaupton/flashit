package steps

import "github.com/kyleaupton/flashit/internal/priv"

// FlashContext holds state shared between Linux flash pipeline steps.
type FlashContext struct {
	ISOPath     string                 // Path to the source ISO file
	TargetDisk  string                 // Target device (e.g., /dev/disk4)
	PrivService priv.PrivilegedService // Privileged service for disk operations
}
