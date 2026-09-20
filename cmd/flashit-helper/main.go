// flashit-helper is the root-side half of FlashIt. It is spawned by the app
// through the platform's elevation mechanism, serves one client over a unix
// socket, and exits when that client leaves or goes idle.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/kyleaupton/flashit/internal/helper"
)

// Version is set at build time via -ldflags="-X main.Version=...".
var Version = "dev"

func main() {
	socket := flag.String("socket", "", "unix socket path to listen on (inside a 0700 directory owned by the caller)")
	idle := flag.Duration("idle", 60*time.Second, "exit after this long without a request")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil)).With("helper", Version)
	if err := run(*socket, *idle, log); err != nil {
		log.Error("exiting", "error", err)
		os.Exit(1)
	}
}

func run(socket string, idle time.Duration, log *slog.Logger) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("must run as root, euid is %d", os.Geteuid())
	}
	if socket == "" {
		return errors.New("-socket is required")
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

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Info("listening", "socket", socket, "caller_uid", uid, "idle", idle)
	srv := helper.New(disk, auth, helper.Options{Version: Version, IdleTimeout: idle, Logger: log})
	err = srv.Serve(ctx, ln)
	switch {
	case errors.Is(err, helper.ErrIdle):
		log.Info("idle, exiting")
		return nil
	case errors.Is(err, context.Canceled):
		log.Info("signalled, exiting")
		return nil
	}
	return err
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
