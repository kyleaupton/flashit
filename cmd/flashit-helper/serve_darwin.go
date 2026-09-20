//go:build darwin

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"time"

	"github.com/kyleaupton/flashit/internal/helper"
)

// serve takes the socketpair end the app placed on fd 3 and serves that one
// session; there is no listener and nothing else can connect.
func serve(ctx context.Context, _ string, idle time.Duration, log *slog.Logger) error {
	f := os.NewFile(3, "app")
	conn, err := net.FileConn(f)
	f.Close()
	if err != nil {
		return fmt.Errorf("fd 3 is not a socket; refusing to serve: %w", err)
	}
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		conn.Close()
		return errors.New("fd 3 is not a unix socket; refusing to serve")
	}
	disk, err := helper.NewDisk(log)
	if err != nil {
		return err
	}
	log.Info("serving", "uid", os.Getuid(), "idle", idle)
	srv := helper.New(disk, nil, helper.Options{Version: Version, IdleTimeout: idle, Logger: log, Authorizer: helper.NewAuthorizer(log)})
	return srv.ServeConn(ctx, uc, helper.Peer{UID: os.Getuid()})
}
