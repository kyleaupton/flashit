//go:build darwin

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/kyleaupton/flashit/internal/helper"
)

const defaultSocket = helper.DefaultSocket

// The client code requirement arrives as plain values via -ldflags -X: the
// app's bundle identifier plus either the SHA-1 of the signing certificate
// (dev builds, self-signed) or the Apple team ID (Developer ID builds).
var (
	AppIdentifier = ""
	LeafSHA1      = ""
	TeamID        = ""
)

func serve(ctx context.Context, socket string, idle time.Duration, log *slog.Logger) error {
	requirement, err := helper.Requirement(AppIdentifier, LeafSHA1, TeamID)
	if err != nil {
		return fmt.Errorf("client code requirement: %w", err)
	}
	auth, err := helper.NewAuth(requirement)
	if err != nil {
		return err
	}
	disk, err := helper.NewDisk(log)
	if err != nil {
		return err
	}
	authz, err := helper.NewAuthorizer(log)
	if err != nil {
		return err
	}
	ln, err := helper.Listen(socket)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer ln.Close()

	log.Info("listening", "socket", socket, "requirement", requirement, "idle", idle)
	srv := helper.New(disk, auth, helper.Options{Version: Version, IdleTimeout: idle, Logger: log, Authorizer: authz})
	err = srv.ServeForever(ctx, ln)
	if errors.Is(err, context.Canceled) {
		log.Info("signalled, exiting")
		return nil
	}
	return err
}
