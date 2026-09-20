# CLAUDE.md

This file provides guidance for Claude Code when working on this project.

Always use Context7 MCP when I need library/API documentation, code generation,
setup or configuration steps without me having to explicitly ask, except for
things that have explicit MCP servers like shadcn-vue.

Documentation IDs:
Wails3 - /websites/v3_wails_io

## Project Overview

FlashIt is a cross-platform desktop app that writes bootable USB installers from
an ISO. Linux ISOs are hybrid images and get written raw to the device. Windows
ISOs are not, so the USB is formatted FAT32 and the ISO contents are copied file
by file, splitting `install.wim` into `.swm` parts when it exceeds the FAT32 4GB
file size limit. Disk work runs through a per-platform privileged helper.

Branch model: `main` is the only long-lived branch. Work happens on feature
branches (`feature/...`, `chore/...`, `fix/...`) and merges to `main` by PR.

## Tech Stack

- Go 1.25 (`go.mod` module `github.com/kyleaupton/flashit`)
- Wails v3.0.0-beta.23 (`github.com/wailsapp/wails/v3`, `@wailsio/runtime`)
- Vue 3 + TypeScript 5.9 + Vite 7, Pinia, Tailwind 4, reka-ui/shadcn-vue
- Task (`Taskfile.yml`) drives build and dev

WIM reading, splitting and LZX decompression are pure Go in `internal/wim`.
There is no CGO and no wimlib dependency anywhere in the Go code.

## Layout

```
main.go, version.go          Wails app entry point, service registration
internal/core/               Shared contracts: Plan, Runnable, Event, Installer
internal/pipeline/           Generic typed pipeline (Step[C], cleanup on failure)
internal/jobs/               Job manager: enqueue, run, cancel
internal/service/            Wails services: JobsService, DrivesService
internal/installers/linux/   Linux installer + its steps/
internal/installers/windows/ Windows installer + its steps/
internal/drives/             Removable drive enumeration per OS (+ mock provider)
internal/iso/                Hybrid ISO validation and ISO mounting per OS
internal/wim/                WIM reader, splitter, lzx/ decompressor
internal/fs/                 File copy, APFS clone on darwin
internal/priv/               Privileged service clients (darwin/linux/windows)
internal/eventbus/           Global emitter wired to app.Event.Emit
internal/logger/             slog wrapper backed by the Wails logger
cmd/wimtest/                 CLI for exercising the WIM splitter
helpers/                     Privileged helper daemons, one per OS (helpers/linux is its own Go module)
frontend/src/                Vue app; frontend/bindings/ is generated and committed
build/                       Per-platform Taskfiles and packaging config
docs/handovers, docs/spikes  Delegated work briefs and spike write-ups
```

## Commands

```bash
task dev                  # hot-reload dev build
task build                # build for the host OS into bin/
go test ./...             # tests live in internal/pipeline, internal/iso, internal/drives (linux-only)
wails3 generate bindings -ts   # regenerate frontend/bindings after changing a service
cd frontend && npm run type-check && npm run build
```

`helpers/linux` is a separate module and only builds for Linux:
`cd helpers/linux && GOOS=linux go build ./...`.

## Status

Host OS support:

| Host    | Drive listing | ISO mount | Privileged helper |
| ------- | ------------- | --------- | ----------------- |
| macOS   | yes (`diskutil`) | yes (`hdiutil`) | launchd + XPC |
| Linux   | yes (`lsblk`) | no, stub returns an error | helper over a socket |
| Windows | yes (PowerShell) | yes | named-pipe helper |

Installer support, derived from `Plan` guards:

| Installer  | macOS | Linux | Windows |
| ---------- | ----- | ----- | ------- |
| Linux ISO  | yes   | yes   | yes     |
| Windows ISO| yes   | no    | yes     |

The Windows installer is unavailable on a Linux host because
`internal/iso/mount_linux_stub.go` reports `IsSupported() == false`, and
`windows.Plan` refuses to build a plan without ISO mounting.

## Job execution flow

`JobsService.StartJob` looks up the installer, calls `installer.Plan`, which
validates the source and drive and returns a `core.Plan` wrapping a bound
`pipeline.Pipeline`. `jobs.Manager.Enqueue` stores the job and runs it on a
cancellable background context. Steps emit through `core.Executor`, the manager
forwards to `eventbus.Emit("job:event", ev)`, and Wails delivers it to the
frontend, where `frontend/src/stores/job.ts` subscribes with `Events.On('job:event', ...)`.

```go
type Event struct {
	JobID   string  `json:"jobId"`
	Type    string  `json:"type"`
	Message string  `json:"message,omitempty"`
	Step    string  `json:"step,omitempty"`
	Percent float64 `json:"percent,omitempty"`
	Error   string  `json:"error,omitempty"`
}
```

The backend emits `Type` values `state`, `step-start`, `step-end`, `progress`
and `log`; a failure arrives as a `state` event with `Error` set. A pipeline
step that implements `CleanupStep` is cleaned up in reverse order when a later
step fails.

## Safety rules

Both installers refuse a target whose ID ends in `disk0`, and require the device
to appear in `drives.ListRemovable`. Enforced in `Plan`:
`internal/installers/linux/linux.go` and
`internal/installers/windows/windows.go`. Both checks are skipped when
`core.DryRun` is set (`DRY_RUN=1`), which also swaps in `drives.MockProvider`.

On macOS the privileged helper rewrites the target to the raw `/dev/rdisk*`
node before writing, for speed (`helpers/darwin/BBPrivilegedHelper.m`).

## Docs

- `docs/handovers/README.md` - how delegated work briefs are written, and the
  open briefs
- `docs/spikes/` - spike write-ups
- `DEV_SETUP.md` - local toolchain setup

`docs/` is gitignored; it is local-only context, not part of the repo.
