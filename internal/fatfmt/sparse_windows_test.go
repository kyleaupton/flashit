package fatfmt

import (
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

// Without the sparse flag NTFS zero-fills up to every write past the valid
// data length, which would mean writing hundreds of GiB for the tail wipe.
func makeSparse(t *testing.T, f *os.File) {
	t.Helper()
	var n uint32
	if err := windows.DeviceIoControl(windows.Handle(f.Fd()), windows.FSCTL_SET_SPARSE, nil, 0, nil, 0, &n, nil); err != nil {
		t.Fatalf("FSCTL_SET_SPARSE: %v", err)
	}
}
