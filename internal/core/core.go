package core

import (
	"context"
	"time"
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

type HostInfo struct {
	OS       OSFamily
	Arch     string
	Version  string
	HasAdmin bool
}

type Capability struct {
	Supported bool
	Reasons   []string
}

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

type SourceMode string

const (
	SourceModeSupply   SourceMode = "supply"
	SourceModeDownload SourceMode = "download"
	SourceModeBoth     SourceMode = "both"
)

type CreateRequest struct {
	Target  Target
	DriveID string
	Options map[string]any
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
	Steps     []Step         `json:"-"`         // Backend only - not serialized (deprecated: use Runnable)
	Runnable  Runnable       `json:"-"`         // Pipeline with typed context (preferred)
	StepInfos []StepInfo     `json:"stepInfos"` // UI metadata for steps
	Meta      map[string]any `json:"meta,omitempty"`
}

type Step interface {
	Name() string
	Run(ctx context.Context, e Executor) error
	Estimate() time.Duration
}

// Runnable is a type-erased interface for executing typed pipelines.
// Pipelines with typed context implement this interface to allow
// the job manager to run them without knowing the context type.
type Runnable interface {
	// StepInfos returns metadata about steps for UI display.
	StepInfos() []StepInfo
	// Run executes the pipeline.
	Run(ctx context.Context, e Executor) error
}

type Installer interface {
	ID() string
	Name() string
	Targets() []Target
	AllowedSources() SourceMode
	ValidateHost(ctx context.Context, host HostInfo) Capability
	Plan(ctx context.Context, req CreateRequest) (*Plan, error)
}

type Event struct {
	JobID   string  `json:"jobId"`
	Type    string  `json:"type"`
	Message string  `json:"message,omitempty"`
	Step    string  `json:"step,omitempty"` // Uses step key from StepInfos
	Percent float64 `json:"percent,omitempty"`
	Error   string  `json:"error,omitempty"`
}

type Executor interface {
	Emit(ev Event)
}
