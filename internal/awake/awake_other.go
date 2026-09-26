//go:build !darwin && !linux && !windows

package awake

import "errors"

func hold(string) (func(), error) { return nil, errors.New("not supported on this platform") }
