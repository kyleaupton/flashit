//go:build unix

package helper

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// removeMountDir removes dir, a directory format_disk mounted a volume on,
// once that volume is gone. root is validate.MountRoot outside tests. Only
// an empty, real directory directly under root that is no longer a
// mountpoint goes, and only through rmdir, which never recurses or follows
// a symlink.
func removeMountDir(root, dir string) error {
	if filepath.Clean(dir) != dir || filepath.Dir(dir) != root {
		return fmt.Errorf("%s is not directly under %s", dir, root)
	}
	rootFi, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if !rootFi.IsDir() {
		return fmt.Errorf("%s is not a directory", root)
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	if fi.Sys().(*syscall.Stat_t).Dev != rootFi.Sys().(*syscall.Stat_t).Dev {
		return fmt.Errorf("%s is still a mountpoint", dir)
	}
	return syscall.Rmdir(dir)
}
