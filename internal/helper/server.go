package helper

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kyleaupton/flashit/internal/proto"
)

const (
	defaultIdleTimeout      = 60 * time.Second
	defaultWriteBufferSize  = 4 << 20
	defaultProgressInterval = 100 * time.Millisecond
	maxLineBytes            = 64 << 10
)

type Options struct {
	Version          string
	IdleTimeout      time.Duration
	WriteBufferSize  int
	ProgressInterval time.Duration
	Logger           *slog.Logger
	// Authorizer gates write_image and format_disk. nil means the host
	// authorized the user before the helper started (polkit, UAC) and no
	// per-op check exists.
	Authorizer Authorizer
}

type Server struct {
	disk Disk
	auth Auth
	opts Options
}

func New(disk Disk, auth Auth, opts Options) *Server {
	if opts.IdleTimeout <= 0 {
		opts.IdleTimeout = defaultIdleTimeout
	}
	if opts.WriteBufferSize <= 0 {
		opts.WriteBufferSize = defaultWriteBufferSize
	}
	if opts.ProgressInterval <= 0 {
		opts.ProgressInterval = defaultProgressInterval
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	return &Server{disk: disk, auth: auth, opts: opts}
}

// Serve accepts one authenticated connection and serves it until it closes.
// Unauthenticated connections are dropped without consuming the slot; any
// connection arriving while one is served is answered with busy and closed.
// It returns ErrIdle when nobody connects or the client goes quiet.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	return s.serve(ctx, ln, false)
}

// ServeForever is Serve for a daemon that outlives its clients: sessions are
// served one after another and an idle client only ends its own session. It
// returns when ctx ends or the listener fails.
func (s *Server) ServeForever(ctx context.Context, ln net.Listener) error {
	return s.serve(ctx, ln, true)
}

func (s *Server) serve(ctx context.Context, ln net.Listener, forever bool) error {
	conns := make(chan net.Conn)
	acceptErr := make(chan error, 1)
	done := make(chan struct{})
	defer close(done)

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				acceptErr <- err
				return
			}
			select {
			case conns <- c:
			case <-done:
				c.Close()
				return
			}
		}
	}()

	var idle <-chan time.Time
	if !forever {
		t := time.NewTimer(s.opts.IdleTimeout)
		defer t.Stop()
		idle = t.C
	}

	// active carries the running session's result; nil means no session.
	var active chan error
	for {
		select {
		case <-ctx.Done():
			if active != nil {
				return <-active
			}
			return ctx.Err()
		case err := <-acceptErr:
			return err
		case <-idle:
			return ErrIdle
		case err := <-active:
			active = nil
			if !forever {
				return err
			}
			s.sessionEnded(err)
		case c := <-conns:
			if active != nil {
				select {
				case err := <-active:
					active = nil
					s.sessionEnded(err)
				default:
				}
			}
			if active != nil {
				s.opts.Logger.Warn("refused second connection")
				s.refuse(c, proto.NewError(proto.CodeBusy, "helper already has a client"))
				continue
			}
			peer, err := s.auth.Authenticate(c)
			if err != nil {
				s.opts.Logger.Warn("rejected connection", "error", err)
				s.refuse(c, proto.NewError(proto.CodeUnauthorized, "caller is not authorized"))
				continue
			}
			s.opts.Logger.Info("client connected", "uid", peer.UID)
			idle = nil
			active = make(chan error, 1)
			go func() { active <- s.ServeConn(ctx, c, peer) }()
		}
	}
}

func (s *Server) sessionEnded(err error) {
	if err != nil && !errors.Is(err, ErrIdle) && !errors.Is(err, context.Canceled) {
		s.opts.Logger.Warn("session ended", "error", err)
		return
	}
	s.opts.Logger.Info("client disconnected")
}

func (s *Server) refuse(c net.Conn, err *proto.Error) {
	_ = c.SetWriteDeadline(time.Now().Add(time.Second))
	_ = writeLine(c, proto.ErrorResponse("", err))
	c.Close()
}

// ServeConn serves an already authenticated connection on behalf of peer.
func (s *Server) ServeConn(ctx context.Context, conn net.Conn, peer Peer) error {
	sess := &session{s: s, conn: conn, peer: peer}
	return sess.serve(ctx)
}

type session struct {
	s    *Server
	conn net.Conn
	peer Peer

	wmu sync.Mutex

	mu        sync.Mutex
	inflight  context.CancelFunc
	handshake bool

	idle      *time.Timer
	idleFired atomic.Bool
	ops       sync.WaitGroup
}

func (sess *session) serve(parent context.Context) error {
	ctx, cancelAll := context.WithCancel(parent)
	defer cancelAll()
	stop := context.AfterFunc(ctx, func() { sess.conn.Close() })
	defer stop()

	sess.idle = time.AfterFunc(sess.s.opts.IdleTimeout, func() {
		sess.idleFired.Store(true)
		sess.conn.Close()
	})
	defer sess.idle.Stop()

	rd := newLineReader(sess.conn)
	var readErr error
	for {
		msg, err := rd.next()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				readErr = err
			}
			break
		}
		sess.idle.Stop()
		var req proto.Request
		if err := json.Unmarshal(msg.line, &req); err != nil {
			closeFDs(msg.fds)
			sess.write(proto.ErrorResponse("", proto.NewError(proto.CodeInvalidRequest, "malformed request")))
			break
		}
		if closeConn := sess.handle(ctx, req, takeFile(msg.fds)); closeConn {
			break
		}
		sess.armIdle()
	}

	sess.conn.Close()
	cancelAll()
	sess.ops.Wait()

	switch {
	case sess.idleFired.Load():
		return ErrIdle
	case parent.Err() != nil:
		return parent.Err()
	case readErr != nil && !errors.Is(readErr, net.ErrClosed) && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrClosedPipe):
		return readErr
	}
	return nil
}

// armIdle restarts the idle timer when nothing is running. Called after every
// control request; an op re-arms it itself under the lock when it finishes.
func (sess *session) armIdle() {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.inflight == nil {
		sess.idle.Reset(sess.s.opts.IdleTimeout)
	}
}

// handle dispatches one request and reports whether the connection must be
// closed afterwards. ping and cancel are answered inline; everything else is
// an op and runs in its own goroutine so cancel can still be read. src is
// the file passed with the request, if any; only write_image keeps it.
func (sess *session) handle(ctx context.Context, req proto.Request, src *os.File) (closeConn bool) {
	log := sess.s.opts.Logger

	if src != nil && req.Op != proto.OpWriteImage {
		log.Warn("descriptor passed with an op that takes none", "op", req.Op)
		src.Close()
		src = nil
	}
	if src != nil {
		defer func() {
			if src != nil {
				src.Close()
			}
		}()
	}

	if req.Op == proto.OpPing {
		var p proto.PingParams
		if err := decodeParams(req, &p); err != nil {
			sess.write(proto.ErrorResponse(req.ID, err))
			return true
		}
		if p.Protocol != proto.ProtocolVersion {
			log.Warn("protocol mismatch", "client", p.Protocol, "helper", proto.ProtocolVersion)
			sess.write(proto.ErrorResponse(req.ID, proto.Errorf(proto.CodeVersionMismatch,
				"client speaks protocol %d, helper speaks %d", p.Protocol, proto.ProtocolVersion)))
			return true
		}
		sess.mu.Lock()
		sess.handshake = true
		sess.mu.Unlock()
		resp, _ := proto.ResultResponse(req.ID, proto.PingResult{
			Protocol: proto.ProtocolVersion,
			Version:  sess.s.opts.Version,
			EUID:     os.Geteuid(),
		})
		sess.write(resp)
		return false
	}

	sess.mu.Lock()
	ready := sess.handshake
	sess.mu.Unlock()
	if !ready {
		sess.write(proto.ErrorResponse(req.ID, proto.NewError(proto.CodeInvalidRequest, "ping must come first")))
		return true
	}

	if req.Op == proto.OpCancel {
		sess.mu.Lock()
		if sess.inflight != nil {
			log.Info("cancelling in-flight op")
			sess.inflight()
		}
		sess.mu.Unlock()
		resp, _ := proto.ResultResponse(req.ID, nil)
		sess.write(resp)
		return false
	}

	sess.mu.Lock()
	if sess.inflight != nil {
		sess.mu.Unlock()
		sess.write(proto.ErrorResponse(req.ID, proto.NewError(proto.CodeBusy, "another op is in flight")))
		return false
	}
	opCtx, cancel := context.WithCancel(ctx)
	sess.inflight = cancel
	// Stopped under the same lock that marks the op running, so a previous
	// op's completion can never re-arm the timer over this one.
	sess.idle.Stop()
	sess.mu.Unlock()

	sess.ops.Add(1)
	image := src
	src = nil
	go func() {
		defer sess.ops.Done()
		defer cancel()
		if image != nil {
			defer image.Close()
		}

		data, err := sess.run(opCtx, req, image)
		var resp proto.Response
		if err != nil {
			pe := toProtoError(opCtx, err)
			log.Error("op failed", "op", req.Op, "id", req.ID, "code", pe.Code, "message", pe.Message)
			resp = proto.ErrorResponse(req.ID, pe)
		} else {
			resp, err = proto.ResultResponse(req.ID, data)
			if err != nil {
				resp = proto.ErrorResponse(req.ID, proto.Errorf(proto.CodeInternal, "encode result: %v", err))
			}
		}

		// Clear inflight and re-arm idle under the write lock so the next op
		// cannot start, and write, before this result reaches the wire.
		sess.wmu.Lock()
		sess.mu.Lock()
		sess.inflight = nil
		sess.idle.Reset(sess.s.opts.IdleTimeout)
		sess.mu.Unlock()
		sess.writeLocked(resp)
		sess.wmu.Unlock()
	}()
	return false
}

func toProtoError(ctx context.Context, err error) *proto.Error {
	var pe *proto.Error
	if errors.As(err, &pe) {
		return pe
	}
	if ctx.Err() != nil || errors.Is(err, context.Canceled) {
		return proto.NewError(proto.CodeCancelled, "operation was cancelled")
	}
	return proto.NewError(proto.CodeInternal, err.Error())
}

func decodeParams(req proto.Request, v any) *proto.Error {
	if len(req.Params) == 0 {
		return nil
	}
	if err := json.Unmarshal(req.Params, v); err != nil {
		return proto.Errorf(proto.CodeInvalidRequest, "bad params for %s: %v", req.Op, err)
	}
	return nil
}

func (sess *session) write(resp proto.Response) {
	sess.wmu.Lock()
	defer sess.wmu.Unlock()
	sess.writeLocked(resp)
}

func (sess *session) writeLocked(resp proto.Response) {
	if err := writeLine(sess.conn, resp); err != nil {
		sess.s.opts.Logger.Warn("write failed", "error", err)
	}
}

func writeLine(w io.Writer, resp proto.Response) error {
	b, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

// throttle limits progress messages to one per interval; the first call
// always passes.
type throttle struct {
	interval time.Duration
	last     time.Time
}

func (t *throttle) allow(now time.Time) bool {
	if !t.last.IsZero() && now.Sub(t.last) < t.interval {
		return false
	}
	t.last = now
	return true
}
