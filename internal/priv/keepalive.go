package priv

import (
	"context"
	"time"

	"github.com/kyleaupton/flashit/internal/core"
	"github.com/kyleaupton/flashit/internal/logger"
)

const keepalivePingTimeout = 5 * time.Second

// keepalive pings one helper session on a timer so its idle timeout cannot
// fire while a job runs. It only ever pings the client it was given: the
// first failed ping ends it, and nothing here can start another helper.
type keepalive struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func startKeepalive(c *Client, every time.Duration) *keepalive {
	ctx, cancel := context.WithCancel(context.Background())
	k := &keepalive{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(k.done)
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			pingCtx, cancelPing := context.WithTimeout(ctx, keepalivePingTimeout)
			_, err := c.Ping(pingCtx)
			cancelPing()
			if err != nil {
				if ctx.Err() == nil {
					logger.Warn("helper keepalive stopped", "error", err)
				}
				return
			}
		}
	}()
	return k
}

// stop ends the keepalive and waits for its last ping to return.
func (k *keepalive) stop() {
	k.cancel()
	<-k.done
}

// HoldDuring keeps svc's session alive for exactly as long as r runs. The
// job manager calls Run once and it returns on success, failure and cancel
// alike, after the pipeline's cleanups, so the hold covers the cleanup
// unmount and cannot outlive the job.
func HoldDuring(svc PrivilegedService, r core.Runnable) core.Runnable {
	return heldRunnable{svc: svc, Runnable: r}
}

type heldRunnable struct {
	core.Runnable
	svc PrivilegedService
}

func (h heldRunnable) Run(ctx context.Context, e core.Executor) error {
	release := h.svc.Hold()
	defer release()
	return h.Runnable.Run(ctx, e)
}
