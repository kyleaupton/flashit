//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/kyleaupton/flashit/internal/helper"
)

func serve(ctx context.Context, socket string, idle time.Duration, log *slog.Logger) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("must run as root, euid is %d", os.Geteuid())
	}
	if socket == "" {
		return errors.New("-socket is required (inside a 0700 directory owned by the caller)")
	}
	uid, err := callerUID()
	if err != nil {
		return err
	}

	disk, err := helper.NewDisk(uid, log)
	if err != nil {
		return err
	}
	auth, err := helper.NewAuth(uid)
	if err != nil {
		return err
	}
	ln, err := helper.Listen(socket, uid)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer ln.Close()

	log.Info("listening", "socket", socket, "caller_uid", uid, "idle", idle)
	srv := helper.New(disk, auth, helper.Options{Version: Version, IdleTimeout: idle, Logger: log})
	return srv.Serve(ctx, ln)
}

// callerUID is the user pkexec elevated for. Without it there is nobody to
// authenticate, so the helper does not start.
func callerUID() (int, error) {
	v, ok := os.LookupEnv("PKEXEC_UID")
	if !ok || v == "" {
		return -1, errors.New("PKEXEC_UID is not set; refusing to serve an unknown caller")
	}
	uid, err := strconv.Atoi(v)
	if err != nil || uid < 0 {
		return -1, fmt.Errorf("PKEXEC_UID %q is not a uid", v)
	}
	return uid, nil
}
