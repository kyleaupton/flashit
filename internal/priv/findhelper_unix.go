//go:build unix && !(linux && production)

package priv

import (
	"fmt"
	"os"
	"path/filepath"
)

// findHelper looks next to the running executable, in its helpers/
// directory (the Taskfile's bin/helpers layout), then at the platform's
// installed paths. Never the working directory: on Linux pkexec would run
// whatever sits there as root. Packaged Linux builds use
// findhelper_linux_production.go instead.
func findHelper() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	exeDir := filepath.Dir(exe)
	candidates := []string{
		filepath.Join(exeDir, helperName),
		filepath.Join(exeDir, "helpers", helperName),
	}
	candidates = append(candidates, installedHelperPaths...)
	for _, p := range candidates {
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			return p, nil
		}
	}
	return "", fmt.Errorf("helper not found at %v", candidates)
}
