//go:build unix

package priv

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"syscall"
)

// checkRootOwned refuses path unless only root can replace what it names:
// the file itself is a regular file, not a symlink, and it and every
// directory up to / are real (not symlinked) and owned by root with no group
// or other write bit. A POSIX ACL granting anyone write shows up in the group
// bits as its mask, so it is refused too. lstat is os.Lstat outside tests.
func checkRootOwned(path string, lstat func(string) (fs.FileInfo, error)) error {
	refuse := func(format string, args ...any) error {
		return fmt.Errorf("refusing to run the privileged helper: "+format+"; reinstall the flashit package", args...)
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return refuse("%q is not a clean absolute path", path)
	}
	fi, err := lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return refuse("%s is not installed", path)
	}
	if err != nil {
		return refuse("%v", err)
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		return refuse("%s is a symlink", path)
	}
	if !fi.Mode().IsRegular() {
		return refuse("%s is not a regular file", path)
	}
	if err := rootOnly(path, fi); err != nil {
		return refuse("%v", err)
	}
	for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
		fi, err := lstat(dir)
		if err != nil {
			return refuse("%v", err)
		}
		if !fi.IsDir() {
			return refuse("%s is not a directory", dir)
		}
		if err := rootOnly(dir, fi); err != nil {
			return refuse("%v", err)
		}
		if dir == "/" {
			return nil
		}
	}
}

func rootOnly(path string, fi fs.FileInfo) error {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("%s has no owner information", path)
	}
	if st.Uid != 0 {
		return fmt.Errorf("%s is owned by uid %d, not root", path, st.Uid)
	}
	if fi.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%s is writable by group or others (mode %v)", path, fi.Mode().Perm())
	}
	return nil
}
