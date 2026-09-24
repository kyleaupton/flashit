// flashit-helper is the disk-writing half of FlashIt. On Linux it is spawned
// by the app through pkexec, serves one client over a unix socket, and exits
// when that client leaves or goes idle. On macOS it is an unprivileged child
// of the app serving the socketpair it inherited on fd 3; the raw device
// arrives per flash from authopen after the authorization sheet.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kyleaupton/flashit/internal/helper"
)

// Version is set at build time via -ldflags="-X main.Version=...".
var Version = "dev"

func main() {
	socket := flag.String("socket", "", "unix socket path to listen on (Linux)")
	idle := flag.Duration("idle", 60*time.Second, "end the session after this long without a request")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil)).With("helper", Version)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	err := serve(ctx, *socket, *idle, log)
	switch {
	case err == nil:
	case errors.Is(err, helper.ErrIdle):
		log.Info("idle, exiting")
	case errors.Is(err, context.Canceled):
		log.Info("signalled, exiting")
	default:
		log.Error("exiting", "error", err)
		os.Exit(1)
	}
}
