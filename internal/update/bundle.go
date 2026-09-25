package update

import (
	"fmt"
	"os"
	"path/filepath"

	"howett.net/plist"
)

const (
	bundleName = "FlashIt.app"
	bundleID   = "dev.kyleupton.flashit"
)

// checkBundleInfo checks the unpacked update is our bundle at the version
// the manifest claimed. The signature covers the archive bytes but not the
// manifest's version, so without this a signed older release could be
// replayed as a newer one.
func checkBundleInfo(path, version string) error {
	if filepath.Base(path) != bundleName {
		return fmt.Errorf("update unpacked to %q, want %s", filepath.Base(path), bundleName)
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s in the update is not a directory", bundleName)
	}
	raw, err := os.ReadFile(filepath.Join(path, "Contents", "Info.plist"))
	if err != nil {
		return err
	}
	var info struct {
		ID         string `plist:"CFBundleIdentifier"`
		Executable string `plist:"CFBundleExecutable"`
		Version    string `plist:"CFBundleShortVersionString"`
	}
	if _, err := plist.Unmarshal(raw, &info); err != nil {
		return fmt.Errorf("Info.plist: %w", err)
	}
	switch {
	case info.ID != bundleID:
		return fmt.Errorf("update is bundle %q, want %s", info.ID, bundleID)
	case info.Executable != "FlashIt":
		return fmt.Errorf("update runs %q, want FlashIt", info.Executable)
	case info.Version != version:
		return fmt.Errorf("update bundle is version %q, the manifest said %q", info.Version, version)
	}
	return nil
}
