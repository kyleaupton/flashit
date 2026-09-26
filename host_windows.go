package main

import (
	"github.com/kyleaupton/flashit/internal/helper"
	"github.com/kyleaupton/flashit/internal/priv"
)

// runPrivilegedHelper serves as the privileged helper when the app started
// this copy of itself elevated for that. It must run before Wails starts.
func runPrivilegedHelper(args []string) (int, bool) {
	if len(args) == 0 || args[0] != priv.HelperFlag {
		return 0, false
	}
	return helper.ServeWindows(args[1:], Version), true
}
