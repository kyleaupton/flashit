//go:build unix

package priv

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

type fakeInfo struct {
	name string
	mode fs.FileMode
	uid  uint32
}

func (f fakeInfo) Name() string       { return f.name }
func (f fakeInfo) Size() int64        { return 0 }
func (f fakeInfo) Mode() fs.FileMode  { return f.mode }
func (f fakeInfo) ModTime() time.Time { return time.Time{} }
func (f fakeInfo) IsDir() bool        { return f.mode.IsDir() }
func (f fakeInfo) Sys() any           { return &syscall.Stat_t{Uid: f.uid} }

type fakeTree map[string]fakeInfo

func (t fakeTree) lstat(path string) (fs.FileInfo, error) {
	if fi, ok := t[path]; ok {
		return fi, nil
	}
	return nil, &fs.PathError{Op: "lstat", Path: path, Err: fs.ErrNotExist}
}

const helperPath = "/usr/libexec/flashit/flashit-helper"

func installedTree() fakeTree {
	return fakeTree{
		"/":                    {name: "/", mode: fs.ModeDir | 0o755},
		"/usr":                 {name: "usr", mode: fs.ModeDir | 0o755},
		"/usr/libexec":         {name: "libexec", mode: fs.ModeDir | 0o755},
		"/usr/libexec/flashit": {name: "flashit", mode: fs.ModeDir | 0o755},
		helperPath:             {name: "flashit-helper", mode: 0o755},
	}
}

func TestCheckRootOwnedAcceptsPackagedLayout(t *testing.T) {
	if err := checkRootOwned(helperPath, installedTree().lstat); err != nil {
		t.Fatal(err)
	}
}

func TestCheckRootOwnedRefuses(t *testing.T) {
	cases := []struct {
		name   string
		path   string
		mutate func(fakeTree)
		want   string
	}{
		{"missing helper", helperPath, func(t fakeTree) { delete(t, helperPath) }, "not installed"},
		{"helper owned by user", helperPath, func(t fakeTree) { t[helperPath] = fakeInfo{mode: 0o755, uid: 1000} }, "uid 1000"},
		{"helper group writable", helperPath, func(t fakeTree) { t[helperPath] = fakeInfo{mode: 0o775} }, "writable"},
		{"helper world writable", helperPath, func(t fakeTree) { t[helperPath] = fakeInfo{mode: 0o757} }, "writable"},
		{"helper symlink", helperPath, func(t fakeTree) { t[helperPath] = fakeInfo{mode: fs.ModeSymlink | 0o777} }, "symlink"},
		{"helper is a directory", helperPath, func(t fakeTree) { t[helperPath] = fakeInfo{mode: fs.ModeDir | 0o755} }, "not a regular file"},
		{"parent owned by user", helperPath, func(t fakeTree) {
			t["/usr/libexec/flashit"] = fakeInfo{mode: fs.ModeDir | 0o755, uid: 1000}
		}, "uid 1000"},
		{"parent group writable", helperPath, func(t fakeTree) {
			t["/usr/libexec/flashit"] = fakeInfo{mode: fs.ModeDir | 0o775}
		}, "writable"},
		{"sticky world-writable ancestor", helperPath, func(t fakeTree) {
			t["/usr"] = fakeInfo{mode: fs.ModeDir | fs.ModeSticky | 0o777}
		}, "writable"},
		{"root writable by others", helperPath, func(t fakeTree) { t["/"] = fakeInfo{mode: fs.ModeDir | 0o757} }, "writable"},
		{"symlinked parent", helperPath, func(t fakeTree) {
			t["/usr/libexec"] = fakeInfo{mode: fs.ModeSymlink | 0o777}
		}, "not a directory"},
		{"relative path", "usr/libexec/flashit/flashit-helper", func(fakeTree) {}, "clean absolute"},
		{"unclean path", "/usr/libexec/../libexec/flashit/flashit-helper", func(fakeTree) {}, "clean absolute"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tree := installedTree()
			c.mutate(tree)
			err := checkRootOwned(c.path, tree.lstat)
			if err == nil {
				t.Fatal("accepted")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error %q does not mention %q", err, c.want)
			}
		})
	}
}

func TestCheckRootOwnedRefusesUserTempFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flashit-helper")
	if err := os.WriteFile(path, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := checkRootOwned(path, os.Lstat); err == nil {
		t.Fatalf("accepted %s", path)
	}
}
