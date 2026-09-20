//go:build !linux

package helper

import (
	"errors"
	"log/slog"
	"net"
)

var errUnsupported = errors.New("helper: this host has no native helper yet")

func NewDisk(int, *slog.Logger) (Disk, error) { return nil, errUnsupported }

func NewAuth(int) (Auth, error) { return nil, errUnsupported }

func Listen(string, int) (net.Listener, error) { return nil, errUnsupported }
