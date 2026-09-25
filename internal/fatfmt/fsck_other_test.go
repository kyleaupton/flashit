//go:build !linux && !darwin

package fatfmt

import (
	"os"
	"testing"
)

// No FAT checker for image files here; checkImage still reads the volume
// back.
func fsck(*testing.T, *os.File, int64, int64) {}
