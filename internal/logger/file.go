package logger

import (
	"os"
	"path/filepath"
)

// Dir is where log files go: %LOCALAPPDATA%\FlashIt\logs on Windows.
func Dir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "FlashIt", "logs"), nil
}

// OpenFile opens name in Dir for appending, creating both as needed.
func OpenFile(name string) (*os.File, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
}
