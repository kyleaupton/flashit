package priv

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kyleaupton/flashit/internal/proto"
)

// cancelGrace is how long a cancelled op may take to acknowledge before the
// client gives up on the connection.
const cancelGrace = 30 * time.Second

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
	resp, err := c.do(ctx, proto.OpPing, proto.PingParams{Protocol: proto.ProtocolVersion}, nil)
	if err != nil {
		return proto.PingResult{}, err
	}
	var r proto.PingResult
	if err := json.Unmarshal(resp.Data, &r); err != nil {
		return proto.PingResult{}, fmt.Errorf("decode ping: %w", err)
	}
	return r, nil
}

func (c *Client) WriteImage(ctx context.Context, device, source string, size int64, progress ProgressFunc) error {
	_, err := c.do(ctx, proto.OpWriteImage, proto.WriteImageParams{Device: device, Source: source, Size: size}, progress)
	return err
}

func (c *Client) FormatDisk(ctx context.Context, device, filesystem, label string) (string, error) {
	resp, err := c.do(ctx, proto.OpFormatDisk, proto.FormatDiskParams{Device: device, Filesystem: filesystem, Label: label}, nil)
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
	_, err := c.do(ctx, proto.OpUnmount, proto.UnmountParams{Device: device}, nil)
	return err
}

func (c *Client) Eject(ctx context.Context, device string) error {
	_, err := c.do(ctx, proto.OpEject, proto.EjectParams{Device: device}, nil)
	return err
}

// Cancel asks the helper to abort whatever op is in flight.
func (c *Client) Cancel(ctx context.Context) error {
	_, err := c.do(ctx, proto.OpCancel, nil, nil)
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
func (c *Client) do(ctx context.Context, op proto.Op, params any, progress ProgressFunc) (proto.Response, error) {
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
	if err := c.send(req); err != nil {
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
			cancelled = true
			ctxDone = nil
			grace = time.After(cancelGrace)
			if op != proto.OpCancel {
				c.sendCancel()
			}
		case <-grace:
			c.conn.Close()
			return proto.Response{}, fmt.Errorf("helper did not acknowledge cancel: %w", ctx.Err())
		}
	}
}

// sendCancel is fire-and-forget: the answer arrives for an ID nobody waits on
// and the read loop drops it.
func (c *Client) sendCancel() {
	req, _ := proto.NewRequest(strconv.FormatUint(c.nextID.Add(1), 10), proto.OpCancel, nil)
	_ = c.send(req)
}

func (c *Client) send(req proto.Request) error {
	b, err := json.Marshal(req)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err = c.conn.Write(append(b, '\n'))
	return err
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
	c.readErr = err
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
}

func (c *Client) connError() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.readErr != nil {
		return c.readErr
	}
	return errors.New("helper connection closed")
}
