//go:build !windows

package fatfmt

import (
	"os"
	"testing"
)

// Truncate leaves a hole on every unix filesystem the tests run on.
func makeSparse(*testing.T, *os.File) {}
