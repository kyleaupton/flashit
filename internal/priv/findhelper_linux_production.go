//go:build linux && production

package priv

import "os"

// findHelper in a packaged build names only the helper the package
// installed, and only while no one but root can change it: pkexec runs what
// this returns as root, under FlashIt's polkit message.
func findHelper() (string, error) {
	if err := checkRootOwned(installedHelper, os.Lstat); err != nil {
		return "", err
	}
	return installedHelper, nil
}
