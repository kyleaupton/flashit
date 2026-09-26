//go:build !windows

package main

// The privileged helper is its own executable here (cmd/flashit-helper).
func runPrivilegedHelper([]string) (int, bool) { return 0, false }
