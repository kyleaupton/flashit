// Package proto holds the wire types shared by the app and the privileged
// helper. It must not import anything else from this repository.
package proto

import (
	"encoding/json"
	"fmt"
)

// ProtocolVersion is sent by the client in ping and checked by the helper.
// Bump it whenever a request or response shape changes incompatibly.
const ProtocolVersion = 3

type Op string

const (
	OpPing       Op = "ping"
	OpWriteImage Op = "write_image"
	OpFormatDisk Op = "format_disk"
	OpMountISO   Op = "mount_iso" // reserved, not implemented yet
	OpUnmount    Op = "unmount"
	OpEject      Op = "eject"
	OpCancel     Op = "cancel"
)

const (
	TypeProgress = "progress"
	TypeResult   = "result"
	TypeError    = "error"
)

type ErrorCode string

const (
	CodeInvalidRequest       ErrorCode = "invalid_request"
	CodeVersionMismatch      ErrorCode = "version_mismatch"
	CodeBusy                 ErrorCode = "busy"
	CodeInvalidDevice        ErrorCode = "invalid_device"
	CodeNotRemovable         ErrorCode = "not_removable"
	CodeSystemDisk           ErrorCode = "system_disk"
	CodeDeviceBusy           ErrorCode = "device_busy"
	CodeInvalidSource        ErrorCode = "invalid_source"
	CodeSizeMismatch         ErrorCode = "size_mismatch"
	CodeInsufficientCapacity ErrorCode = "insufficient_capacity"
	CodeInvalidLabel         ErrorCode = "invalid_label"
	CodeCancelled            ErrorCode = "cancelled"
	CodeUnauthorized         ErrorCode = "unauthorized"
	CodeInternal             ErrorCode = "internal"
)

// Request is one NDJSON line from the app to the helper.
type Request struct {
	ID     string          `json:"id"`
	Op     Op              `json:"op"`
	Params json.RawMessage `json:"params,omitempty"`
}

// Response is one NDJSON line from the helper to the app. Which fields are
// meaningful depends on Type.
type Response struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"`
	Written uint64          `json:"written,omitempty"`
	Total   uint64          `json:"total,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
	Code    ErrorCode       `json:"code,omitempty"`
	Message string          `json:"message,omitempty"`
}

type PingParams struct {
	Protocol int `json:"protocol"`
}

type PingResult struct {
	Protocol int    `json:"protocol"`
	Version  string `json:"version"`
	EUID     int    `json:"euid"`
}

// WriteImageParams names the target; the image itself travels as an open
// file descriptor passed with the request line over the unix socket
// (SCM_RIGHTS), so the helper reads exactly the file the app opened and
// never opens a path as root. Size is what the app measured and the helper
// checks. Authorization, on macOS, is the base64 external form of an
// AuthorizationRef the helper redeems for the write right; Linux ignores it.
type WriteImageParams struct {
	Device        string `json:"device"`
	Size          int64  `json:"size"`
	Authorization string `json:"authorization,omitempty"`
}

// FormatDiskParams carries Authorization as WriteImageParams does.
type FormatDiskParams struct {
	Device        string `json:"device"`
	Filesystem    string `json:"filesystem"`
	Label         string `json:"label"`
	Authorization string `json:"authorization,omitempty"`
}

type FormatDiskResult struct {
	Mountpoint string `json:"mountpoint"`
}

type MountISOParams struct {
	Path string `json:"path"`
}

type MountISOResult struct {
	Mountpoint string `json:"mountpoint"`
}

// UnmountParams names either a mountpoint or a device, not both.
type UnmountParams struct {
	Mountpoint string `json:"mountpoint,omitempty"`
	Device     string `json:"device,omitempty"`
}

type EjectParams struct {
	Device string `json:"device"`
}

// Error is a helper-side failure carried over the wire. It is what the client
// returns for an error response and what validation produces in the helper.
type Error struct {
	Code    ErrorCode
	Message string
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

func NewError(code ErrorCode, msg string) *Error { return &Error{Code: code, Message: msg} }

func Errorf(code ErrorCode, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// NewRequest marshals params into a Request. A nil params leaves Params empty.
func NewRequest(id string, op Op, params any) (Request, error) {
	req := Request{ID: id, Op: op}
	if params == nil {
		return req, nil
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return Request{}, err
	}
	req.Params = raw
	return req, nil
}

func ProgressResponse(id string, written, total uint64) Response {
	return Response{ID: id, Type: TypeProgress, Written: written, Total: total}
}

// ResultResponse marshals data into a result response. A nil data leaves Data
// empty.
func ResultResponse(id string, data any) (Response, error) {
	resp := Response{ID: id, Type: TypeResult}
	if data == nil {
		return resp, nil
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return Response{}, err
	}
	resp.Data = raw
	return resp, nil
}

func ErrorResponse(id string, err *Error) Response {
	return Response{ID: id, Type: TypeError, Code: err.Code, Message: err.Message}
}

// Err converts an error response back into an *Error. It returns nil for any
// other response type.
func (r Response) Err() *Error {
	if r.Type != TypeError {
		return nil
	}
	return &Error{Code: r.Code, Message: r.Message}
}
