//go:build !darwin

package priv

// Only macOS installs a daemon the user has to approve.

func HelperStatus() (string, error) { return "ready", nil }

func OpenHelperSettings() error { return nil }
