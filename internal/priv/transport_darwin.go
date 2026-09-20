//go:build darwin

package priv

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/kyleaupton/flashit/internal/logger"
	"github.com/kyleaupton/flashit/internal/proto"
)

const (
	dialTimeout = 2 * time.Second
	pingTimeout = 5 * time.Second
	// connectBudget bounds one attempt to reach a usable helper, so a job
	// start never freezes the UI for longer; the caller gets a retriable
	// error and the daemon keeps coming up in the background.
	connectBudget = 30 * time.Second
	// How long a freshly registered daemon gets to bind its socket, and how
	// long a registered one that is not answering gets before the app
	// concludes launchd cannot start it.
	socketGrace  = 15 * time.Second
	restartGrace = 5 * time.Second
	// register() keeps failing with EPERM for a while after an unregister.
	// That patience is only spent right after an unregister of our own; a
	// fresh registration that keeps failing is reported quickly.
	registerAfterUnregister = 25 * time.Second
	registerFresh           = 6 * time.Second
	registerInterval        = 2 * time.Second
	dialInterval            = 250 * time.Millisecond
)

var (
	errStaleHelper = errors.New("helper is not the one in this bundle")
	errHelperBusy  = errors.New("another FlashIt instance is using the helper")

	// ErrHelperNotReady means the daemon is being registered or restarted and
	// did not come up within the budget; the same request will work once it
	// has.
	ErrHelperNotReady = errors.New("FlashIt's helper is still starting; try again in a minute")
)

// legacyHelperPlist is where the SMJobBless helper of earlier builds was
// installed, under the same launchd label. SMAppService reports it as
// enabled, so it has to go before this daemon can be registered.
const legacyHelperPlist = "/Library/LaunchDaemons/" + proto.DarwinHelperLabel + ".plist"

// connectHelper returns a client whose helper answered ping as root, speaks
// this protocol and carries this build's version. Anything else means the
// daemon launchd runs is missing, stale or unlaunchable, and the fix is the
// same each time: register it again from this bundle. It returns within
// connectBudget.
func connectHelper(parent context.Context) (*Client, error) {
	ctx, cancel := context.WithTimeout(parent, connectBudget)
	defer cancel()
	c, err := connectOnce(ctx)
	if err != nil && parent.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
		logger.Warn("helper did not come up within the budget", "error", err)
		return nil, ErrHelperNotReady
	}
	return c, err
}

func connectOnce(ctx context.Context) (*Client, error) {
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
		} else if ctx.Err() != nil {
			return nil, err
		}
	case smNotFound, smNotRegistered:
		if err := registerUntilDone(ctx, registerFresh); err != nil {
			return nil, err
		}
		return dialUntil(ctx, socketGrace)
	}

	logger.Info("re-registering the helper")
	if err := smUnregister(); err != nil {
		logger.Warn("unregister failed, registering anyway", "error", err)
	}
	if err := registerUntilDone(ctx, registerAfterUnregister); err != nil {
		return nil, err
	}
	return dialUntil(ctx, socketGrace)
}

// registerUntilDone registers the daemon, retrying EPERM for up to patience.
// Approval pending is the user's move and is reported at once.
func registerUntilDone(ctx context.Context, patience time.Duration) error {
	deadline := time.Now().Add(patience)
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
			return fmt.Errorf("%w (status %s: %v)", ErrHelperNotReady, st, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(registerInterval):
		}
	}
}

// dialUntil keeps dialing for up to patience. busy is retried too: the
// daemon answers busy for a moment while it tears down the session a
// previous client of ours just closed.
func dialUntil(ctx context.Context, patience time.Duration) (*Client, error) {
	deadline := time.Now().Add(patience)
	for {
		c, err := dialAndPing(ctx)
		if err == nil {
			return c, nil
		}
		if time.Now().After(deadline) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(dialInterval):
		}
	}
}

// dialAndPing connects and runs the handshake. A helper that refuses the
// connection, speaks another protocol or reports another version is stale.
func dialAndPing(ctx context.Context) (*Client, error) {
	conn, err := net.DialTimeout("unix", proto.DarwinSocketPath, dialTimeout)
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
	if info.Version != HelperVersion {
		c.Close()
		return nil, fmt.Errorf("%w: helper is %q, this build wants %q", errStaleHelper, info.Version, HelperVersion)
	}
	return c, nil
}
