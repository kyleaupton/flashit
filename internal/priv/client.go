package priv

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kyleaupton/flashit/internal/proto"
)

// cancelGrace is how long a cancelled op may take to acknowledge before the
// client gives up on the connection. A variable so tests can shorten it.
var cancelGrace = 30 * time.Second

// Client speaks the helper protocol over any net.Conn. Responses are demuxed
// by request ID so a cancel can be sent while an op streams progress.
type Client struct {
	conn net.Conn
	wmu  sync.Mutex

	mu      sync.Mutex
	pending map[string]chan proto.Response
	readErr error

	nextID atomic.Uint64
	done   chan struct{}
}

func NewClient(conn net.Conn) *Client {
	c := &Client{conn: conn, pending: map[string]chan proto.Response{}, done: make(chan struct{})}
	go c.readLoop()
	return c
}

// Ping performs the version handshake. A helper that speaks a different
// protocol answers with version_mismatch and closes the connection.
func (c *Client) Ping(ctx context.Context) (proto.PingResult, error) {
	resp, err := c.do(ctx, proto.OpPing, proto.PingParams{Protocol: proto.ProtocolVersion}, nil, nil)
	if err != nil {
		return proto.PingResult{}, err
	}
	var r proto.PingResult
	if err := json.Unmarshal(resp.Data, &r); err != nil {
		return proto.PingResult{}, fmt.Errorf("decode ping: %w", err)
	}
	return r, nil
}

// WriteImage streams image onto p.Device. The open file itself is handed to
// the helper with the request (a passed descriptor on unix, a handle value
// the helper duplicates on Windows), so the helper writes exactly this file;
// the caller keeps ownership and closes it afterwards.
func (c *Client) WriteImage(ctx context.Context, p proto.WriteImageParams, image *os.File, progress ProgressFunc) error {
	if image == nil {
		return errors.New("write_image needs an open image")
	}
	p.Handle = imageHandle(image)
	_, err := c.do(ctx, proto.OpWriteImage, p, []int{int(image.Fd())}, progress)
	return err
}

func (c *Client) FormatDisk(ctx context.Context, p proto.FormatDiskParams) (string, error) {
	resp, err := c.do(ctx, proto.OpFormatDisk, p, nil, nil)
	if err != nil {
		return "", err
	}
	var r proto.FormatDiskResult
	if err := json.Unmarshal(resp.Data, &r); err != nil {
		return "", fmt.Errorf("decode format result: %w", err)
	}
	return r.Mountpoint, nil
}

func (c *Client) Unmount(ctx context.Context, device string) error {
	_, err := c.do(ctx, proto.OpUnmount, proto.UnmountParams{Device: device}, nil, nil)
	return err
}

func (c *Client) Eject(ctx context.Context, device string) error {
	_, err := c.do(ctx, proto.OpEject, proto.EjectParams{Device: device}, nil, nil)
	return err
}

// Cancel asks the helper to abort whatever op is in flight.
func (c *Client) Cancel(ctx context.Context) error {
	_, err := c.do(ctx, proto.OpCancel, nil, nil, nil)
	return err
}

func (c *Client) Close() error {
	err := c.conn.Close()
	<-c.done
	return err
}

// do sends one request and waits for its result or error, forwarding
// progress. When ctx ends first it sends cancel and keeps waiting for the
// op's own answer, so the caller never returns while the helper still writes.
func (c *Client) do(ctx context.Context, op proto.Op, params any, fds []int, progress ProgressFunc) (proto.Response, error) {
	id := strconv.FormatUint(c.nextID.Add(1), 10)
	ch := make(chan proto.Response, 64)

	c.mu.Lock()
	if c.readErr != nil {
		c.mu.Unlock()
		return proto.Response{}, c.readErr
	}
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	req, err := proto.NewRequest(id, op, params)
	if err != nil {
		return proto.Response{}, err
	}
	if err := c.send(req, fds); err != nil {
		// The write can fail before the read loop has recorded why the
		// helper hung up; let it catch up so the caller sees that reason.
		select {
		case <-c.done:
		case <-time.After(time.Second):
		}
		if cerr := c.readError(); cerr != nil {
			return proto.Response{}, cerr
		}
		return proto.Response{}, err
	}

	ctxDone := ctx.Done()
	var grace <-chan time.Time
	cancelled := false
	for {
		select {
		case resp, ok := <-ch:
			if !ok {
				return proto.Response{}, c.connError()
			}
			switch resp.Type {
			case proto.TypeProgress:
				if progress != nil {
					progress(resp.Written, resp.Total)
				}
			case proto.TypeResult:
				return resp, nil
			case proto.TypeError:
				if cancelled && resp.Code == proto.CodeCancelled {
					return resp, ctx.Err()
				}
				return resp, resp.Err()
			default:
				return resp, fmt.Errorf("unknown response type %q", resp.Type)
			}
		case <-ctxDone:
			// A ping is answered inline and cancels nothing; sending cancel
			// for it would abort whatever op is in flight.
			if op == proto.OpPing {
				return proto.Response{}, ctx.Err()
			}
			cancelled = true
			ctxDone = nil
			grace = time.After(cancelGrace)
			if op != proto.OpCancel {
				c.sendCancel()
			}
		case <-grace:
			// Closing ends the read loop; wait for it so Broken is true by
			// the time the caller looks.
			c.conn.Close()
			<-c.done
			return proto.Response{}, fmt.Errorf("helper did not acknowledge cancel: %w", ctx.Err())
		}
	}
}

// sendCancel is fire-and-forget: the answer arrives for an ID nobody waits on
// and the read loop drops it.
func (c *Client) sendCancel() {
	req, _ := proto.NewRequest(strconv.FormatUint(c.nextID.Add(1), 10), proto.OpCancel, nil)
	_ = c.send(req, nil)
}

func (c *Client) send(req proto.Request, fds []int) error {
	b, err := json.Marshal(req)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return writeWithFDs(c.conn, append(b, '\n'), fds)
}

func (c *Client) readLoop() {
	defer close(c.done)
	r := bufio.NewReader(c.conn)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			c.fail(fmt.Errorf("helper connection lost: %w", err))
			return
		}
		var resp proto.Response
		if err := json.Unmarshal(line, &resp); err != nil {
			c.fail(fmt.Errorf("malformed response from helper: %w", err))
			c.conn.Close()
			return
		}
		// Refusals the helper writes before any request has an ID (busy,
		// unauthorized, malformed) end the session; surface them as the
		// connection error so every waiter sees the real code.
		if resp.ID == "" {
			if pe := resp.Err(); pe != nil {
				c.fail(fmt.Errorf("helper refused the connection: %w", pe))
				c.conn.Close()
				return
			}
			continue
		}
		c.mu.Lock()
		ch := c.pending[resp.ID]
		c.mu.Unlock()
		if ch != nil {
			ch <- resp
		}
	}
}

func (c *Client) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.readErr != nil {
		return
	}
	c.readErr = err
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
}

// Broken reports whether the connection is gone: the helper hung up, sent
// garbage, or the client closed it on a cancel the helper never answered.
func (c *Client) Broken() bool { return c.readError() != nil }

func (c *Client) readError() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.readErr
}

func (c *Client) connError() error {
	if err := c.readError(); err != nil {
		return err
	}
	return errors.New("helper connection closed")
}
