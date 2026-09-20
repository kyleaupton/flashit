//go:build !darwin

package priv

// Only macOS gates the raw device behind a permission the user can revoke.
func OpenPrivacySettings() error { return nil }
