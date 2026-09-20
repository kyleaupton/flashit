//go:build unix

package validate

import (
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kyleaupton/flashit/internal/proto"
)

type fakeInfo struct {
	mode fs.FileMode
	size int64
	uid  int
}

func (f fakeInfo) Name() string       { return "x" }
func (f fakeInfo) Size() int64        { return f.size }
func (f fakeInfo) Mode() fs.FileMode  { return f.mode }
func (f fakeInfo) ModTime() time.Time { return time.Time{} }
func (f fakeInfo) IsDir() bool        { return f.mode.IsDir() }
func (f fakeInfo) Sys() any           { return &syscall.Stat_t{Uid: uint32(f.uid)} }

type noSysInfo struct{ fakeInfo }

func (noSysInfo) Sys() any { return nil }

func TestSource(t *testing.T) {
	const caller = 1000
	cases := []struct {
		name   string
		fi     fs.FileInfo
		caller int
		size   int64
		want   proto.ErrorCode
	}{
		{"regular file, owned, size matches", fakeInfo{0o644, 4096, caller}, caller, 4096, ""},
		{"directory", fakeInfo{fs.ModeDir | 0o755, 4096, caller}, caller, 4096, proto.CodeInvalidSource},
		{"block device", fakeInfo{fs.ModeDevice | 0o600, 4096, caller}, caller, 4096, proto.CodeInvalidSource},
		{"char device", fakeInfo{fs.ModeDevice | fs.ModeCharDevice, 4096, caller}, caller, 4096, proto.CodeInvalidSource},
		{"fifo", fakeInfo{fs.ModeNamedPipe, 4096, caller}, caller, 4096, proto.CodeInvalidSource},
		{"owned by root", fakeInfo{0o644, 4096, 0}, caller, 4096, proto.CodeInvalidSource},
		{"owned by another user", fakeInfo{0o644, 4096, caller + 1}, caller, 4096, proto.CodeInvalidSource},
		{"world readable but not owned", fakeInfo{0o644, 4096, 0}, caller, 4096, proto.CodeInvalidSource},
		{"caller unknown", fakeInfo{0o644, 4096, caller}, -1, 4096, proto.CodeInvalidSource},
		{"owner unknown", noSysInfo{fakeInfo{0o644, 4096, caller}}, caller, 4096, proto.CodeInvalidSource},
		{"zero size", fakeInfo{0o644, 0, caller}, caller, 0, proto.CodeInvalidSource},
		{"negative size", fakeInfo{0o644, 4096, caller}, caller, -1, proto.CodeInvalidSource},
		{"size too small", fakeInfo{0o644, 4096, caller}, caller, 4095, proto.CodeSizeMismatch},
		{"size too large", fakeInfo{0o644, 4096, caller}, caller, 4097, proto.CodeSizeMismatch},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := Source(c.fi, c.caller, c.size)
			wantCode(t, err, c.want)
			if err != nil && c.fi.Size() != 0 && strings.Contains(err.Error(), strconv.FormatInt(c.fi.Size(), 10)) {
				t.Fatalf("message leaks the real size: %v", err)
			}
		})
	}
}

// An unowned file must be refused before the size is compared, so a probe
// with a wrong size learns nothing from the code either.
func TestSourceOwnershipBeforeSize(t *testing.T) {
	wantCode(t, Source(fakeInfo{0o644, 4096, 0}, 1000, 1), proto.CodeInvalidSource)
}

func TestSourceWithRealFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.iso")
	if err := os.WriteFile(path, make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(path)
	me := os.Getuid()
	wantCode(t, Source(fi, me, 100), "")
	wantCode(t, Source(fi, me+1, 100), proto.CodeInvalidSource)
	wantCode(t, Source(fi, me, 99), proto.CodeSizeMismatch)
	di, _ := os.Stat(dir)
	wantCode(t, Source(di, me, di.Size()), proto.CodeInvalidSource)
}
