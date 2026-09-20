//go:build darwin

package priv

import (
	"context"
	"errors"
	"net"
	"os"
	"testing"
	"time"

	"github.com/kyleaupton/flashit/internal/helper"
	"github.com/kyleaupton/flashit/internal/helper/helpertest"
	"github.com/kyleaupton/flashit/internal/proto"
)

// With FLASHIT_TEST_HELPER set the test binary acts as the helper: it serves
// fd 3 with the real server on a fake disk, exactly as flashit-helper does.
func TestMain(m *testing.M) {
	switch os.Getenv("FLASHIT_TEST_HELPER") {
	case "serve":
		os.Exit(fakeHelper())
	case "exit":
		os.Exit(3)
	}
	os.Exit(m.Run())
}

func fakeHelper() int {
	f := os.NewFile(3, "app")
	conn, err := net.FileConn(f)
	f.Close()
	if err != nil {
		return 2
	}
	srv := helper.New(helpertest.NewFakeDisk(), nil, helper.Options{Version: "fake", IdleTimeout: 10 * time.Second})
	if err := srv.ServeConn(context.Background(), conn.(*net.UnixConn), helper.Peer{UID: os.Getuid()}); err != nil && !errors.Is(err, helper.ErrIdle) {
		return 1
	}
	return 0
}

func TestSpawnHelperServesFD3(t *testing.T) {
	t.Setenv("FLASHIT_TEST_HELPER", "serve")
	proc, err := spawnHelper(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	c := NewClient(proc.conn)
	ctx := ctxTimeout(t)
	info, err := c.Ping(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.Version != "fake" || info.Protocol != proto.ProtocolVersion {
		t.Fatalf("ping result %+v", info)
	}
	if err := c.Eject(ctx, helpertest.Removable); err != nil {
		t.Fatal(err)
	}
	// Closing the app's end is the helper's cue to exit on its own.
	c.Close()
	proc.release()
	if !proc.wait(5 * time.Second) {
		t.Fatal("helper did not exit after the session closed")
	}
}

func TestEnsureReadySpawnsAndRespawns(t *testing.T) {
	t.Setenv("FLASHIT_TEST_HELPER", "serve")
	s := &darwinService{findHelper: func() (string, error) { return os.Args[0], nil }}
	ctx := ctxTimeout(t)
	if err := s.EnsureReady(ctx); err != nil {
		t.Fatal(err)
	}
	first := s.proc
	if err := s.Disk().Eject(ctx, helpertest.Removable); err != nil {
		t.Fatal(err)
	}
	// A live helper is reused.
	if err := s.EnsureReady(ctx); err != nil {
		t.Fatal(err)
	}
	if s.proc != first {
		t.Fatal("EnsureReady replaced a live helper")
	}
	// A helper that went away is replaced.
	first.cmd.Process.Kill()
	first.wait(5 * time.Second)
	if err := s.EnsureReady(ctx); err != nil {
		t.Fatal(err)
	}
	second := s.proc
	if second == first {
		t.Fatal("EnsureReady kept a dead helper")
	}
	if err := s.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if !second.wait(5 * time.Second) {
		t.Fatal("helper did not exit after Shutdown")
	}
	if s.Disk().Eject(ctx, helpertest.Removable) == nil {
		t.Fatal("Disk usable after Shutdown")
	}
}

func TestEnsureReadyReportsHelperFailures(t *testing.T) {
	ctx := ctxTimeout(t)
	s := &darwinService{findHelper: func() (string, error) { return "/nonexistent/flashit-helper", nil }}
	if err := s.EnsureReady(ctx); err == nil {
		t.Fatal("spawned a helper that does not exist")
	}

	t.Setenv("FLASHIT_TEST_HELPER", "exit")
	s = &darwinService{findHelper: func() (string, error) { return os.Args[0], nil }}
	err := s.EnsureReady(ctx)
	if err == nil {
		t.Fatal("handshake succeeded with a helper that exited at once")
	}
	if s.client != nil {
		t.Fatal("client kept after a failed handshake")
	}
}
