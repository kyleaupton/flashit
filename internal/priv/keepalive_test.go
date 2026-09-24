package priv

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/kyleaupton/flashit/internal/core"
	"github.com/kyleaupton/flashit/internal/helper"
	"github.com/kyleaupton/flashit/internal/helper/helpertest"
	"github.com/kyleaupton/flashit/internal/proto"
)

func connectIdle(t *testing.T, idle time.Duration) (*Client, <-chan error) {
	t.Helper()
	srv := helper.New(helpertest.NewFakeDisk(), helpertest.FakeAuth{}, helper.Options{Version: "test", IdleTimeout: idle})
	cc, sc := helpertest.SocketPair(t)
	done := make(chan error, 1)
	go func() { done <- srv.ServeConn(context.Background(), sc, helper.Peer{UID: os.Getuid()}) }()
	c := NewClient(cc)
	t.Cleanup(func() { c.Close() })
	if _, err := c.Ping(ctxTimeout(t)); err != nil {
		t.Fatal(err)
	}
	return c, done
}

func TestKeepaliveOutlastsIdleTimeout(t *testing.T) {
	const idle = 150 * time.Millisecond
	c, served := connectIdle(t, idle)

	k := startKeepalive(c, idle/3)
	select {
	case err := <-served:
		t.Fatalf("helper exited during the job: %v", err)
	case <-time.After(5 * idle):
	}
	k.stop()

	select {
	case err := <-served:
		if !errors.Is(err, helper.ErrIdle) {
			t.Fatalf("helper returned %v, want ErrIdle", err)
		}
	case <-time.After(10 * idle):
		t.Fatal("helper did not idle out after the keepalive stopped")
	}
	k.stop()
}

func TestKeepaliveStopsOnFailedPing(t *testing.T) {
	c, served := connectIdle(t, 50*time.Millisecond)
	if err := <-served; !errors.Is(err, helper.ErrIdle) {
		t.Fatalf("helper returned %v, want ErrIdle", err)
	}

	k := startKeepalive(c, 10*time.Millisecond)
	select {
	case <-k.done:
	case <-time.After(5 * time.Second):
		t.Fatal("keepalive kept going after the helper was gone")
	}
	if !c.Broken() {
		t.Fatal("client should report the lost helper")
	}
}

type holdService struct {
	PrivilegedService
	mu       sync.Mutex
	held     int
	released int
}

func (s *holdService) Hold() func() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.held++
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.released++
	}
}

type runFunc func(ctx context.Context) error

func (runFunc) StepInfos() []core.StepInfo                       { return nil }
func (f runFunc) Run(ctx context.Context, _ core.Executor) error { return f(ctx) }

func TestHoldDuringCoversTheRun(t *testing.T) {
	for _, want := range []error{nil, errors.New("step failed"), context.Canceled} {
		svc := &holdService{}
		r := HoldDuring(svc, runFunc(func(context.Context) error {
			if svc.held != 1 || svc.released != 0 {
				t.Errorf("hold not active during the run: held %d released %d", svc.held, svc.released)
			}
			return want
		}))
		if err := r.Run(context.Background(), nil); !errors.Is(err, want) {
			t.Fatalf("got %v, want %v", err, want)
		}
		if svc.held != 1 || svc.released != 1 {
			t.Fatalf("after %v: held %d released %d", want, svc.held, svc.released)
		}
	}
}

// A ping that times out must not send cancel: that would abort the op the
// helper is running.
func TestPingTimeoutDoesNotCancelInflightOp(t *testing.T) {
	disk := helpertest.NewFakeDisk()
	disk.Raw = &helpertest.FakeRaw{Started: make(chan struct{}), Block: make(chan struct{})}
	c, _ := connect(t, disk)
	if _, err := c.Ping(ctxTimeout(t)); err != nil {
		t.Fatal(err)
	}

	const size = 4096
	wrote := make(chan error, 1)
	go func() {
		wrote <- c.WriteImage(ctxTimeout(t), proto.WriteImageParams{Device: helpertest.Removable, Size: size}, openSource(t, size), nil)
	}()
	<-disk.Raw.Started

	pingCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Ping(pingCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("ping returned %v", err)
	}
	close(disk.Raw.Block)
	if err := <-wrote; err != nil {
		t.Fatalf("write was disturbed by the ping: %v", err)
	}
}
