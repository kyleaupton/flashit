package main

// Protocol version for client-helper communication.
// IMPORTANT: This file is intentionally duplicated in internal/priv/linux/protocol.go
// because the helper is a separate Go module. Any changes here MUST be mirrored there.
const ProtocolVersion = 1

// Request represents a command sent from the main app to the privileged helper.
type Request struct {
	// ID is a unique identifier for this request, used to correlate responses.
	ID string `json:"id"`

	// Command is the operation to perform.
	// Valid values: "write_iso", "format_disk", "cancel", "shutdown", "ping"
	Command string `json:"command"`

	// Device is the target device path (e.g., "/dev/sdb").
	// Required for: write_iso, format_disk
	Device string `json:"device,omitempty"`

	// ISOPath is the path to the ISO file to write.
	// Required for: write_iso
	ISOPath string `json:"iso_path,omitempty"`

	// Filesystem is the filesystem type for formatting.
	// Valid values: "FAT32", "NTFS", "exFAT"
	// Required for: format_disk
	Filesystem string `json:"filesystem,omitempty"`

	// VolumeName is the volume label for the formatted disk.
	// Required for: format_disk
	VolumeName string `json:"volume_name,omitempty"`

	// TargetID is the request ID to cancel.
	// Required for: cancel
	TargetID string `json:"target_id,omitempty"`
}

// Response represents a message from the privileged helper back to the main app.
type Response struct {
	// ID is the request ID this response corresponds to.
	ID string `json:"id"`

	// Type indicates the response type.
	// Valid values: "progress", "result", "error"
	Type string `json:"type"`

	// Success indicates whether the operation completed successfully.
	// Only valid when Type == "result".
	Success bool `json:"success,omitempty"`

	// Written is the number of bytes written so far.
	// Only valid when Type == "progress".
	Written uint64 `json:"written,omitempty"`

	// Total is the total number of bytes to write.
	// Only valid when Type == "progress".
	Total uint64 `json:"total,omitempty"`

	// Error contains the error message if the operation failed.
	// Only valid when Type == "error" or (Type == "result" && !Success).
	Error string `json:"error,omitempty"`
}

// Command constants for Request.Command field.
const (
	CmdWriteISO   = "write_iso"
	CmdFormatDisk = "format_disk"
	CmdCancel     = "cancel"
	CmdShutdown   = "shutdown"
	CmdPing       = "ping"
)

// Response type constants for Response.Type field.
const (
	RespTypeProgress = "progress"
	RespTypeResult   = "result"
	RespTypeError    = "error"
)
