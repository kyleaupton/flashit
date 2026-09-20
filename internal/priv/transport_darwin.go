//go:build darwin

package priv

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kyleaupton/flashit/internal/logger"
	"github.com/kyleaupton/flashit/internal/proto"
)

const (
	socketPath  = "/var/run/dev.kyleupton.flashit.sock"
	dialTimeout = 2 * time.Second
	pingTimeout = 5 * time.Second
	// How long a freshly registered daemon gets to bind its socket before
	// the app gives up on it, and how long a registered one that is not
	// answering gets before the app concludes launchd cannot start it.
	socketGrace  = 15 * time.Second
	restartGrace = 5 * time.Second
	// register() keeps failing for a while after an unregister; the app
	// keeps asking until it succeeds or this runs out.
	registerPatience = 3 * time.Minute
	registerInterval = 2 * time.Second
)

var (
	errStaleHelper = errors.New("helper is not the one in this bundle")
	errHelperBusy  = errors.New("another FlashIt instance is using the helper")
)

// legacyHelperPlist is where the SMJobBless helper of earlier builds was
// installed, under the same launchd label. SMAppService reports it as
// enabled, so it has to go before this daemon can be registered.
const legacyHelperPlist = "/Library/LaunchDaemons/dev.kyleupton.flashit.helper.plist"

// connectHelper returns a client whose helper answered ping as root, speaks
// this protocol and carries this build's version. Anything else means the
// daemon launchd runs is missing, stale or unlaunchable, and the fix is the
// same each time: register it again from this bundle.
func connectHelper(ctx context.Context) (*Client, error) {
	c, err := dialAndPing(ctx)
	if err == nil {
		return c, nil
	}
	if errors.Is(err, errHelperBusy) {
		return nil, err
	}
	logger.Info("helper not usable, checking its registration", "error", err)

	switch st := smDaemonStatus(); st {
	case smRequiresApproval:
		return nil, ErrHelperNeedsApproval
	case smEnabled:
		if _, statErr := os.Stat(legacyHelperPlist); statErr == nil {
			return nil, fmt.Errorf("an old FlashIt helper is installed at %s; remove it (see DEV_SETUP.md) and try again", legacyHelperPlist)
		}
		if errors.Is(err, errStaleHelper) {
			break
		}
		// Registered and supposedly running; launchd may still be starting
		// it. If it never binds, its binary was replaced and launchd will not
		// launch it until it is registered again.
		logger.Info("helper is registered but not answering, waiting for it")
		if c, err := dialUntil(ctx, restartGrace); err == nil {
			return c, nil
		}
	case smNotFound, smNotRegistered:
		if err := registerUntilDone(ctx); err != nil {
			return nil, err
		}
		return dialUntil(ctx, socketGrace)
	}

	logger.Info("re-registering the helper")
	if err := smUnregister(); err != nil {
		logger.Warn("unregister failed, registering anyway", "error", err)
	}
	if err := registerUntilDone(ctx); err != nil {
		return nil, err
	}
	return dialUntil(ctx, socketGrace)
}

// registerUntilDone registers the daemon. EPERM right after an unregister
// means launchd is not ready yet; approval pending is the user's move.
func registerUntilDone(ctx context.Context) error {
	deadline := time.Now().Add(registerPatience)
	for {
		err := smRegister()
		st := smDaemonStatus()
		logger.Info("register helper", "status", st, "error", err)
		if err == nil && st == smEnabled {
			return nil
		}
		if st == smRequiresApproval {
			return ErrHelperNeedsApproval
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("could not register the helper (status %s): %w", st, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(registerInterval):
		}
	}
}

func dialUntil(ctx context.Context, patience time.Duration) (*Client, error) {
	deadline := time.Now().Add(patience)
	for {
		c, err := dialAndPing(ctx)
		if err == nil {
			return c, nil
		}
		if errors.Is(err, errHelperBusy) || time.Now().After(deadline) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// dialAndPing connects and runs the handshake. A helper that refuses the
// connection, speaks another protocol or reports another version is stale.
func dialAndPing(ctx context.Context) (*Client, error) {
	conn, err := net.DialTimeout("unix", socketPath, dialTimeout)
	if err != nil {
		return nil, fmt.Errorf("connect to helper: %w", err)
	}
	c := NewClient(conn)
	pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	info, err := c.Ping(pingCtx)
	cancel()
	if err != nil {
		c.Close()
		var pe *proto.Error
		if errors.As(err, &pe) {
			switch pe.Code {
			case proto.CodeBusy:
				return nil, errHelperBusy
			case proto.CodeVersionMismatch, proto.CodeUnauthorized:
				return nil, fmt.Errorf("%w: %v", errStaleHelper, err)
			}
		}
		return nil, fmt.Errorf("helper handshake: %w", err)
	}
	if info.EUID != 0 {
		c.Close()
		return nil, fmt.Errorf("helper is running as uid %d, not root", info.EUID)
	}
	if want := expectedHelperVersion(); info.Version != want {
		c.Close()
		return nil, fmt.Errorf("%w: helper is %q, bundle wants %q", errStaleHelper, info.Version, want)
	}
	return c, nil
}

// expectedHelperVersion is what the helper in this bundle was built with.
// The build writes it to Contents/Resources/helper-version so a dev build,
// whose app and helper both call themselves "dev", still notices a rebuilt
// helper. Without the file the app's own version is the answer.
func expectedHelperVersion() string {
	exe, err := os.Executable()
	if err != nil {
		return HelperVersion
	}
	b, err := os.ReadFile(filepath.Join(filepath.Dir(exe), "..", "Resources", "helper-version"))
	if err != nil {
		return HelperVersion
	}
	if v := strings.TrimSpace(string(b)); v != "" {
		return v
	}
	return HelperVersion
}
