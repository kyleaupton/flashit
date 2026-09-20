// flashit-helper is the root-side half of FlashIt. On Linux it is spawned by
// the app through pkexec, serves one client over a unix socket, and exits
// when that client leaves or goes idle. On macOS it is a launchd daemon
// registered by the app, listens on a fixed socket, and serves clients one
// at a time for as long as it runs.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// Version is set at build time via -ldflags="-X main.Version=...".
var Version = "dev"

func main() {
	socket := flag.String("socket", defaultSocket, "unix socket path to listen on")
	idle := flag.Duration("idle", 60*time.Second, "end a client's session after this long without a request")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil)).With("helper", Version)
	if os.Geteuid() != 0 {
		log.Error("exiting", "error", fmt.Sprintf("must run as root, euid is %d", os.Geteuid()))
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := serve(ctx, *socket, *idle, log); err != nil {
		log.Error("exiting", "error", err)
		os.Exit(1)
	}
}
