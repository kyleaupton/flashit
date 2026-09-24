//go:build unix

package helper

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoveMountDir(t *testing.T) {
	root := t.TempDir()
	mk := func(name string) string {
		p := filepath.Join(root, name)
		if err := os.Mkdir(p, 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}

	empty := mk("ESD-USB")
	if err := removeMountDir(root, empty); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(empty); !os.IsNotExist(err) {
		t.Fatalf("empty dir still there: %v", err)
	}

	full := mk("FULL")
	if err := os.WriteFile(filepath.Join(full, "keep"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(mk("OUTER"), "INNER")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	link := filepath.Join(root, "LINK")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "FILE")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	for _, dir := range []string{
		full,
		nested,
		link,
		file,
		root,
		root + "/FULL/",
		root + "/../" + filepath.Base(root) + "/FULL",
		filepath.Join(root, "MISSING"),
		target,
	} {
		if err := removeMountDir(root, dir); err == nil {
			t.Errorf("removed %s", dir)
		}
	}
	for _, p := range []string{full, filepath.Join(full, "keep"), nested, link, target, file} {
		if _, err := os.Lstat(p); err != nil {
			t.Errorf("%s is gone: %v", p, err)
		}
	}

	// A symlinked root is refused even when the directory inside is fine.
	linkedRoot := filepath.Join(t.TempDir(), "root")
	if err := os.Symlink(root, linkedRoot); err != nil {
		t.Fatal(err)
	}
	inside := mk("VIA-LINK")
	if err := removeMountDir(linkedRoot, filepath.Join(linkedRoot, "VIA-LINK")); err == nil {
		t.Error("removed a directory through a symlinked root")
	}
	if _, err := os.Lstat(inside); err != nil {
		t.Errorf("%s is gone: %v", inside, err)
	}
}

// /dev is its own filesystem on Linux and macOS, so it stands in for a
// mountpoint that is still mounted.
func TestRemoveMountDirRefusesMountpoint(t *testing.T) {
	err := removeMountDir("/", "/dev")
	if err == nil || !strings.Contains(err.Error(), "still a mountpoint") {
		t.Fatalf("got %v, want a mountpoint refusal", err)
	}
}
