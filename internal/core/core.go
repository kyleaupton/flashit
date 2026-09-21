package core

import (
	"context"
)

// DryRun controls whether real disk operations are performed.
// When true, installers should return mock steps and drives are simulated.
// Set via DRY_RUN environment variable.
var DryRun bool

type OSFamily string

const (
	OSWindows OSFamily = "windows"
	OSLinux   OSFamily = "linux"
	OSMacOS   OSFamily = "macos"
)

type Target struct {
	Family  OSFamily
	Version string
	Arch    string
}

type SourceSpec struct {
	URL      string
	Checksum string
	Local    string
}

type CreateRequest struct {
	DriveID string
	Source  SourceSpec
}

// StepInfo provides metadata about a step for UI display
type StepInfo struct {
	Key         string `json:"key"`         // Unique key like "writing-iso", "unmounting-disk"
	Name        string `json:"name"`        // Human-readable name like "Writing ISO to USB"
	HasProgress bool   `json:"hasProgress"` // true for long operations with progress tracking
}

type Plan struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Runnable  Runnable       `json:"-"`
	StepInfos []StepInfo     `json:"stepInfos"`
	Meta      map[string]any `json:"meta,omitempty"`
}

// Runnable is a type-erased interface for executing typed pipelines.
// Pipelines with typed context implement this interface to allow
// the job manager to run them without knowing the context type.
type Runnable interface {
	StepInfos() []StepInfo
	Run(ctx context.Context, e Executor) error
}

type Installer interface {
	ID() string
	Name() string
	Targets() []Target
	Plan(ctx context.Context, req CreateRequest) (*Plan, error)
}

type Event struct {
	JobID   string  `json:"jobId"`
	Type    string  `json:"type"`
	Message string  `json:"message,omitempty"`
	Step    string  `json:"step,omitempty"` // Uses step key from StepInfos
	Percent float64 `json:"percent,omitempty"`
	Error   string  `json:"error,omitempty"`
	// Code is the helper's error code when a failure came from it (a
	// proto.ErrorCode), so the frontend can act on it without parsing Error.
	Code string `json:"code,omitempty"`
}

type Executor interface {
	Emit(ev Event)
}
