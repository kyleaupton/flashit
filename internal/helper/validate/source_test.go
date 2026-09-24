package validate

import (
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kyleaupton/flashit/internal/proto"
)

type fakeInfo struct {
	mode fs.FileMode
	size int64
}

func (f fakeInfo) Name() string       { return "x" }
func (f fakeInfo) Size() int64        { return f.size }
func (f fakeInfo) Mode() fs.FileMode  { return f.mode }
func (f fakeInfo) ModTime() time.Time { return time.Time{} }
func (f fakeInfo) IsDir() bool        { return f.mode.IsDir() }
func (f fakeInfo) Sys() any           { return nil }

func TestSource(t *testing.T) {
	cases := []struct {
		name string
		fi   fs.FileInfo
		size int64
		want proto.ErrorCode
	}{
		{"regular file, size matches", fakeInfo{0o644, 4096}, 4096, ""},
		{"directory", fakeInfo{fs.ModeDir | 0o755, 4096}, 4096, proto.CodeInvalidSource},
		{"block device", fakeInfo{fs.ModeDevice | 0o600, 4096}, 4096, proto.CodeInvalidSource},
		{"char device", fakeInfo{fs.ModeDevice | fs.ModeCharDevice, 4096}, 4096, proto.CodeInvalidSource},
		{"fifo", fakeInfo{fs.ModeNamedPipe, 4096}, 4096, proto.CodeInvalidSource},
		{"socket", fakeInfo{fs.ModeSocket, 4096}, 4096, proto.CodeInvalidSource},
		{"zero size", fakeInfo{0o644, 0}, 0, proto.CodeInvalidSource},
		{"negative size", fakeInfo{0o644, 4096}, -1, proto.CodeInvalidSource},
		{"size too small", fakeInfo{0o644, 4096}, 4095, proto.CodeSizeMismatch},
		{"size too large", fakeInfo{0o644, 4096}, 4097, proto.CodeSizeMismatch},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := Source(c.fi, c.size)
			wantCode(t, err, c.want)
			if err != nil && c.fi.Size() != 0 && strings.Contains(err.Error(), strconv.FormatInt(c.fi.Size(), 10)) {
				t.Fatalf("message leaks the real size: %v", err)
			}
		})
	}
}

func TestSourceWithRealFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.iso")
	if err := os.WriteFile(path, make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(path)
	wantCode(t, Source(fi, 100), "")
	wantCode(t, Source(fi, 99), proto.CodeSizeMismatch)
	di, _ := os.Stat(dir)
	wantCode(t, Source(di, di.Size()), proto.CodeInvalidSource)
}
