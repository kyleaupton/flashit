//go:build linux

package linux

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

const (
	socketPathPrefix = "/tmp/flashit-helper-"
	helperName       = "flashit-helper"
)

// socketClient implements the Client interface using Unix domain sockets.
type socketClient struct {
	socketPath string
	helperPath string
	helperCmd  *exec.Cmd
	conn       net.Conn

	mu        sync.Mutex
	writeMu   sync.Mutex // serializes writes to the socket
	ready     bool
	requestID atomic.Uint64

	reader *bufio.Reader

	// helperDone receives the result of helperCmd.Wait() — used to avoid calling Wait() twice
	helperDone chan error

	// cancelMu protects the current operation's cancel channel
	cancelMu     sync.Mutex
	cancelChan   chan struct{}
	currentReqID string
}

// NewClient creates a new Linux privileged helper client.
func NewClient() Client {
	return &socketClient{
		socketPath: socketPathPrefix + uuid.New().String() + ".sock",
	}
}

// EnsureReady spawns the helper process with pkexec elevation if not already running,
// and verifies communication is working.
func (c *socketClient) EnsureReady(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.ready {
		return nil
	}

	// 1. Locate the helper executable
	helperPath, err := c.findHelperPath()
	if err != nil {
		return fmt.Errorf("failed to find helper: %w", err)
	}
	c.helperPath = helperPath

	// 2. Spawn helper with pkexec elevation
	if err := c.spawnViaPkexec(ctx); err != nil {
		return fmt.Errorf("failed to spawn elevated helper: %w", err)
	}

	// 3. Wait for socket to become available
	if err := c.waitForSocket(ctx); err != nil {
		c.killHelper()
		return fmt.Errorf("failed waiting for socket: %w", err)
	}

	// 4. Connect to the Unix socket
	if err := c.connectToSocket(ctx); err != nil {
		c.killHelper()
		return fmt.Errorf("failed to connect to socket: %w", err)
	}

	// 5. Create buffered reader
	c.reader = bufio.NewReader(c.conn)

	// 6. Send a ping to verify communication
	pingReq := Request{
		ID:      c.nextRequestID(),
		Command: CmdPing,
	}

	resp, err := c.sendRequestUnlocked(ctx, pingReq)
	if err != nil {
		c.closeConnUnlocked()
		c.killHelper()
		return fmt.Errorf("ping failed: %w", err)
	}

	if resp.Type == RespTypeError || !resp.Success {
		c.closeConnUnlocked()
		c.killHelper()
		return fmt.Errorf("ping failed: %s", resp.Error)
	}

	c.ready = true
	return nil
}

// findHelperPath locates the helper executable.
func (c *socketClient) findHelperPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	exeDir := filepath.Dir(exe)

	candidates := []string{
		filepath.Join(exeDir, helperName),
		filepath.Join(exeDir, "helpers", helperName),
		filepath.Join(exeDir, "bin", "helpers", helperName),
		filepath.Join(".", "bin", "helpers", helperName),
	}

	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return filepath.Abs(path)
		}
	}

	return "", fmt.Errorf("helper executable not found in any of: %v", candidates)
}

// spawnViaPkexec spawns the helper process with pkexec for privilege elevation.
func (c *socketClient) spawnViaPkexec(ctx context.Context) error {
	c.helperCmd = exec.CommandContext(ctx, "pkexec", c.helperPath, c.socketPath)
	c.helperCmd.Stdout = os.Stderr // helper logs go to stderr
	c.helperCmd.Stderr = os.Stderr

	if err := c.helperCmd.Start(); err != nil {
		// Check if pkexec itself failed (not found, auth denied, etc.)
		return fmt.Errorf("pkexec failed: %w", err)
	}

	// Monitor for early exit (auth denied = exit code 126, dismissed = 126)
	c.helperDone = make(chan error, 1)
	go func() {
		c.helperDone <- c.helperCmd.Wait()
	}()

	// Give it a brief moment to catch immediate failures (auth denied)
	select {
	case err := <-c.helperDone:
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) && exitErr.ExitCode() == 126 {
				return fmt.Errorf("authorization denied by user")
			}
			return fmt.Errorf("helper exited immediately: %w", err)
		}
		return fmt.Errorf("helper exited immediately without error")
	case <-time.After(500 * time.Millisecond):
		// Helper is still running — good, pkexec auth was accepted
		return nil
	}
}

// waitForSocket polls until the socket file appears.
func (c *socketClient) waitForSocket(ctx context.Context) error {
	timeout := time.After(30 * time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout:
			return errors.New("timeout waiting for helper to create socket")
		case <-ticker.C:
			if _, err := os.Stat(c.socketPath); err == nil {
				return nil
			}
		}
	}
}

// connectToSocket connects to the Unix domain socket.
func (c *socketClient) connectToSocket(_ context.Context) error {
	conn, err := net.Dial("unix", c.socketPath)
	if err != nil {
		return fmt.Errorf("dial failed: %w", err)
	}
	c.conn = conn
	return nil
}

// WriteISO writes an ISO image to a raw disk device.
func (c *socketClient) WriteISO(ctx context.Context, isoPath string, device string, progress ProgressFunc) error {
	c.mu.Lock()
	if !c.ready {
		c.mu.Unlock()
		return ErrHelperNotRunning
	}
	c.mu.Unlock()

	reqID := c.nextRequestID()

	// Set up cancellation tracking
	c.cancelMu.Lock()
	c.currentReqID = reqID
	c.cancelChan = make(chan struct{})
	cancelCh := c.cancelChan
	c.cancelMu.Unlock()

	defer func() {
		c.cancelMu.Lock()
		c.currentReqID = ""
		c.cancelChan = nil
		c.cancelMu.Unlock()
	}()

	// Set up context cancellation
	done := make(chan struct{})
	defer close(done)

	go func() {
		select {
		case <-ctx.Done():
			c.CancelCurrentOperation()
		case <-cancelCh:
		case <-done:
		}
	}()

	req := Request{
		ID:      reqID,
		Command: CmdWriteISO,
		Device:  device,
		ISOPath: isoPath,
	}

	resp, err := c.sendRequestWithProgress(ctx, req, progress)
	if err != nil {
		return err
	}

	if resp.Type == RespTypeError || !resp.Success {
		if resp.Error != "" {
			if resp.Error == "cancelled: operation was cancelled" {
				return ErrCancelled
			}
			return errors.New(resp.Error)
		}
		return errors.New("operation failed")
	}

	return nil
}

// FormatDisk formats a disk with the specified filesystem and volume name.
func (c *socketClient) FormatDisk(ctx context.Context, device string, filesystem string, volumeName string) error {
	c.mu.Lock()
	if !c.ready {
		c.mu.Unlock()
		return ErrHelperNotRunning
	}
	c.mu.Unlock()

	reqID := c.nextRequestID()

	req := Request{
		ID:         reqID,
		Command:    CmdFormatDisk,
		Device:     device,
		Filesystem: filesystem,
		VolumeName: volumeName,
	}

	resp, err := c.sendRequest(ctx, req)
	if err != nil {
		return err
	}

	if resp.Type == RespTypeError || !resp.Success {
		if resp.Error != "" {
			return errors.New(resp.Error)
		}
		return errors.New("format failed")
	}

	return nil
}

// CancelCurrentOperation cancels the currently running operation (if any).
func (c *socketClient) CancelCurrentOperation() {
	c.cancelMu.Lock()
	reqID := c.currentReqID
	ch := c.cancelChan
	c.cancelMu.Unlock()

	if reqID == "" || ch == nil {
		return
	}

	// Signal the cancel channel
	select {
	case <-ch:
		// Already cancelled
	default:
		close(ch)
	}

	// Send cancel command to helper (fire-and-forget)
	req := Request{
		ID:       c.nextRequestID(),
		Command:  CmdCancel,
		TargetID: reqID,
	}

	_ = c.writeRequest(req)
}

// Shutdown gracefully terminates the helper process.
func (c *socketClient) Shutdown(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.ready {
		return nil
	}

	req := Request{
		ID:      c.nextRequestID(),
		Command: CmdShutdown,
	}

	// Send shutdown command
	resp, err := c.sendRequestUnlocked(ctx, req)
	if err != nil {
		c.closeConnUnlocked()
		c.ready = false
		c.cleanupSocket()
		return fmt.Errorf("shutdown request failed: %w", err)
	}

	if resp.Type == RespTypeError {
		c.closeConnUnlocked()
		c.ready = false
		c.cleanupSocket()
		return fmt.Errorf("shutdown failed: %s", resp.Error)
	}

	// Wait for helper process to exit
	c.waitForHelperExit(5 * time.Second)

	c.closeConnUnlocked()
	c.ready = false
	c.cleanupSocket()
	return nil
}

// nextRequestID generates a unique request ID.
func (c *socketClient) nextRequestID() string {
	return fmt.Sprintf("req-%d", c.requestID.Add(1))
}

// sendRequest sends a request and waits for the result response.
func (c *socketClient) sendRequest(ctx context.Context, req Request) (*Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sendRequestUnlocked(ctx, req)
}

// sendRequestUnlocked sends a request without acquiring the mutex.
func (c *socketClient) sendRequestUnlocked(ctx context.Context, req Request) (*Response, error) {
	if c.conn == nil {
		return nil, ErrHelperNotRunning
	}

	if err := c.writeRequest(req); err != nil {
		return nil, fmt.Errorf("failed to write request: %w", err)
	}

	resp, err := c.readResponse()
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	return resp, nil
}

// sendRequestWithProgress sends a request and processes progress updates.
func (c *socketClient) sendRequestWithProgress(ctx context.Context, req Request, progress ProgressFunc) (*Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return nil, ErrHelperNotRunning
	}

	if err := c.writeRequest(req); err != nil {
		return nil, fmt.Errorf("failed to write request: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		resp, err := c.readResponse()
		if err != nil {
			return nil, fmt.Errorf("failed to read response: %w", err)
		}

		switch resp.Type {
		case RespTypeProgress:
			if progress != nil {
				progress(resp.Written, resp.Total)
			}
		case RespTypeResult, RespTypeError:
			return resp, nil
		default:
			return nil, fmt.Errorf("unknown response type: %s", resp.Type)
		}
	}
}

// writeRequest encodes and writes a request to the socket.
// Thread-safe: acquires writeMu to serialize all socket writes.
func (c *socketClient) writeRequest(req Request) error {
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to encode request: %w", err)
	}
	data = append(data, '\n')

	c.writeMu.Lock()
	n, err := c.conn.Write(data)
	c.writeMu.Unlock()

	if err != nil {
		return fmt.Errorf("write failed: %w", err)
	}
	if n != len(data) {
		return fmt.Errorf("incomplete write: wrote %d of %d bytes", n, len(data))
	}

	return nil
}

// readResponse reads and decodes a response from the socket.
func (c *socketClient) readResponse() (*Response, error) {
	line, err := c.reader.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("failed to read response line: %w", err)
	}

	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &resp, nil
}

// closeConnUnlocked closes the socket connection. Caller must hold the mutex.
func (c *socketClient) closeConnUnlocked() {
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
	c.reader = nil
}

// killHelper terminates the helper process.
func (c *socketClient) killHelper() {
	if c.helperCmd != nil && c.helperCmd.Process != nil {
		c.helperCmd.Process.Kill()
		c.helperCmd = nil
	}
}

// waitForHelperExit waits for the helper process to exit with a timeout.
func (c *socketClient) waitForHelperExit(timeout time.Duration) {
	if c.helperDone == nil {
		return
	}

	select {
	case <-c.helperDone:
		// Exited cleanly
	case <-time.After(timeout):
		// Force kill
		if c.helperCmd != nil && c.helperCmd.Process != nil {
			c.helperCmd.Process.Kill()
		}
	}
}

// cleanupSocket removes the socket file.
func (c *socketClient) cleanupSocket() {
	os.Remove(c.socketPath)
}
