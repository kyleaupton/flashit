//go:build !darwin

package update

import "errors"

// CheckBundle refuses everything: only macOS installs updates.
func CheckBundle(path, version string) error {
	return errors.New("updates are only installed on macOS")
}

func CanInstall() error {
	return errors.New("updates are only installed on macOS")
}
