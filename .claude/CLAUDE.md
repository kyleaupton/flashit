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

WIM reading, splitting and LZX decompression are pure Go in `internal/wim`;
there is no wimlib dependency. cgo is used only on macOS: the helper's
Authorization/DiskArbitration bindings in `internal/helper/*_darwin.{c,h}`
and the app's AuthorizationRef shim in `internal/priv/authz_darwin.{c,h}`.

## Layout

```
main.go, version.go          Wails app entry point, service registration
internal/core/               Shared contracts: Plan, Runnable, Event, Installer
internal/pipeline/           Generic typed pipeline (Step[C], cleanup on failure)
internal/jobs/               Job manager: enqueue, run, cancel
internal/service/            Wails services: JobsService, DrivesService, PrivService
internal/installers/linux/   Linux installer + its steps/
internal/installers/windows/ Windows installer + its steps/
internal/drives/             Removable drive enumeration per OS (+ mock provider)
internal/iso/                Hybrid ISO validation and ISO mounting per OS
internal/wim/                WIM reader, splitter, lzx/ decompressor
internal/fs/                 File copy, APFS clone on darwin
internal/proto/              Wire types shared by the app and the Go helper (NDJSON, protocol v2)
internal/helper/             Helper server: validate/, ops, one-op-at-a-time; disk_linux.go + auth_linux.go are the Linux bindings, {disk,authz,authopen}_darwin.go + diskutil.go the macOS ones, helpertest/ holds fakes
internal/priv/               Privileged service clients: client.go (shared protocol client), transport_linux.go + service_linux.go (pkexec), transport_darwin.go + service_darwin.go + authz_darwin.c (child helper, authopen), windows/ (old helper)
internal/eventbus/           Global emitter wired to app.Event.Emit
internal/logger/             slog wrapper backed by the Wails logger
cmd/flashit-helper/          Go helper entry point: serve_linux.go (pkexec, one session) and serve_darwin.go (child of the app, serves fd 3)
cmd/wimtest/                 CLI for exercising the WIM splitter
helpers/windows/             Old C privileged helper for Windows, still shipped
frontend/src/                Vue app; frontend/bindings/ is generated and committed
build/                       Per-platform Taskfiles and packaging config
docs/handovers, docs/spikes  Delegated work briefs and spike write-ups
```

## Commands

```bash
task dev                  # hot-reload dev build; on macOS assembles and signs bin/FlashIt.dev.app with the helper inside (see DEV_SETUP.md)
task build                # build for the host OS into bin/
task darwin:package       # release bundle bin/FlashIt.app (needs APPLE_TEAM_ID; APPLE_SIGNING_IDENTITY or ad hoc)
go test -race ./...       # tests live in internal/proto, internal/helper, internal/priv, internal/pipeline, internal/iso, internal/drives (linux-only)
GOOS=linux go build ./cmd/flashit-helper   # cross-compile the Go helper; `task linux:build:helper` puts it in bin/helpers/
wails3 generate bindings -ts      # regenerate frontend/bindings after changing a service
cd frontend && npm run build      # main.go embeds frontend/dist, so build it before any go build
cd frontend && npm run type-check
```

`GOOS=linux go build ./...` fails on macOS because the Wails Linux backend
needs cgo; cross-check with `GOOS=linux go build ./internal/... ./cmd/...`.

## Status

Host OS support:

| Host    | Drive listing | ISO mount | Privileged helper |
| ------- | ------------- | --------- | ----------------- |
| macOS   | yes (`diskutil`) | yes (`hdiutil`) | `cmd/flashit-helper` spawned from the bundle as an unprivileged child, socketpair on fd 3; the raw device comes from `authopen` per flash |
| Linux   | yes (`lsblk`) | no, stub returns an error | `cmd/flashit-helper` spawned via `pkexec`, unix socket |
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

The Go helper trusts nothing the app says. `internal/helper/validate`
rejects device paths outside `/dev` or containing `..`, partitions, non-block
nodes, non-removable disks, any whole disk backing the running system,
images larger than the device, and labels outside `^[A-Za-z0-9_ -]{1,11}$`.
Only `fat32` formats. Validation runs before any prompt, so a refused
request never costs the user a sheet.

The helper never opens an image by path. The app opens the ISO itself and
passes the open descriptor with the `write_image` line over the unix socket
(`SCM_RIGHTS`); the helper fstats what it received, requires a regular file
of the claimed size, and streams from it. That is the confused-deputy fix:
the helper can only write what the app could already read, so no ownership
check is needed (and none would work on macOS external volumes, which mount
with ownership ignored). A request without a descriptor, or one whose
descriptor is a pipe, directory or device, is refused; extra descriptors are
closed unread.

Linux: removable means sysfs `removable` or a USB ancestor; system disks are
resolved from `/`, `/boot`, `/home` and friends through dm/md slaves (btrfs
and ZFS roots through the mount source), fail closed. The caller's
`SO_PEERCRED` uid must equal `PKEXEC_UID`; the socket lives in a 0700
directory the app creates.

macOS: nothing runs as root and there is no daemon, socket file or peer
check; the helper is the app's own child on an inherited socketpair, so the
only client is the parent. Removable means `diskutil` reports RemovableMedia
or Ejectable, not Internal, and not Virtual (disk images and synthesized APFS
containers are refused). The system disks are the physical stores behind the
APFS container mounted at `/`. `write_image` and `format_disk` carry the
external form of an unauthorized `AuthorizationRef` the app created. After
validation the helper probes `/dev/rdiskN` read-only (EPERM is the user
refusing Removable Volumes, reported as `tcc_denied`; EACCES is normal), then
calls `AuthorizationCopyRights` on that ref for Apple's
`sys.openfile.readwrite./dev/rdiskN` with interaction allowed: one password
sheet per flash, named after the app, right before the destructive step.
Only a ref that passed that check reaches `/usr/libexec/authopen -extauth`,
which sends the open descriptor back over `SCM_RIGHTS`; a bad form would
make authopen raise a second sheet. The authorized form is a bearer
credential for a root read-write open of any path for the rule's timeout, so
the helper composes the authopen path from its own validated `DeviceInfo`
(never from the request), the form never leaves the socketpair, and the ref
is destroyed (`kAuthorizationFlagDestroyRights`) the moment `OpenRaw`
returns. Raw writes go through that descriptor under a Disk Arbitration
claim; unmount, FAT32 erase and eject are unprivileged `diskutil` calls.

## Docs

- `docs/handovers/README.md` - how delegated work briefs are written, and the
  open briefs
- `docs/spikes/` - spike write-ups
- `DEV_SETUP.md` - macOS dev certificate, Removable Volumes prompt, clean-slate commands

`docs/` is gitignored; it is local-only context, not part of the repo.
