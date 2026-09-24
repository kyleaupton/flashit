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
	switch mode := os.Getenv("FLASHIT_TEST_HELPER"); mode {
	case "serve", "deny", "hang":
		os.Exit(fakeHelper(mode))
	case "exit":
		os.Exit(3)
	}
	os.Exit(m.Run())
}

// fakeHelper serves fd 3 like flashit-helper. "deny" answers every
// authorized op with tcc_denied; "hang" never answers the sheet.
func fakeHelper(mode string) int {
	f := os.NewFile(3, "app")
	conn, err := net.FileConn(f)
	f.Close()
	if err != nil {
		return 2
	}
	opts := helper.Options{Version: "fake", IdleTimeout: 10 * time.Second}
	switch mode {
	case "deny":
		opts.Authorizer = &helpertest.FakeAuthorizer{Err: proto.NewError(proto.CodeTCCDenied, "removable volumes refused")}
	case "hang":
		opts.Authorizer = &helpertest.FakeAuthorizer{Block: make(chan struct{})}
	}
	srv := helper.New(helpertest.NewFakeDisk(), nil, opts)
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
	// Ops ensure their own helper, so one after Shutdown spawns again.
	if err := s.Disk().Eject(ctx, helpertest.Removable); err != nil {
		t.Fatal(err)
	}
	if s.proc == nil || s.proc == second {
		t.Fatal("op after Shutdown did not spawn a fresh helper")
	}
	_ = s.Shutdown(ctx)
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

// A TCC verdict is cached per process, so a denied helper is retired and the
// next op gets a fresh one that will ask again.
func TestTCCDenialRetiresHelper(t *testing.T) {
	t.Setenv("FLASHIT_TEST_HELPER", "deny")
	s := &darwinService{findHelper: func() (string, error) { return os.Args[0], nil }}
	ctx := ctxTimeout(t)
	if err := s.EnsureReady(ctx); err != nil {
		t.Fatal(err)
	}
	denied := s.proc
	err := s.Disk().WriteISO(ctx, sourceFile(t, 4096), helpertest.Removable, nil)
	wantCode(t, err, proto.CodeTCCDenied)
	if s.client != nil || s.proc != nil {
		t.Fatal("denied helper was kept")
	}
	if !denied.wait(5 * time.Second) {
		t.Fatal("denied helper still running")
	}

	t.Setenv("FLASHIT_TEST_HELPER", "serve")
	if err := s.Disk().Eject(ctx, helpertest.Removable); err != nil {
		t.Fatal(err)
	}
	if s.proc == nil || s.proc == denied {
		t.Fatal("no fresh helper for the retry")
	}
}

// A helper blocked in the sheet cannot acknowledge a cancel; once the client
// gives up on the connection the process is killed, and the sheet with it.
func TestUnansweredCancelKillsHelper(t *testing.T) {
	t.Setenv("FLASHIT_TEST_HELPER", "hang")
	old := cancelGrace
	cancelGrace = 200 * time.Millisecond
	t.Cleanup(func() { cancelGrace = old })

	s := &darwinService{findHelper: func() (string, error) { return os.Args[0], nil }}
	if err := s.EnsureReady(ctxTimeout(t)); err != nil {
		t.Fatal(err)
	}
	stuck := s.proc
	ctx, cancel := context.WithCancel(ctxTimeout(t))
	result := make(chan error, 1)
	go func() { result <- s.Disk().FormatDisk(ctx, helpertest.Removable, "fat32", "X") }()
	time.Sleep(200 * time.Millisecond)
	cancel()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("format succeeded behind an unanswered sheet")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("op did not return after cancel")
	}
	if !stuck.wait(5 * time.Second) {
		t.Fatal("helper still running behind the sheet")
	}
	if s.client != nil || s.proc != nil {
		t.Fatal("stuck helper was kept")
	}
}
