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

- Go 1.26 (`go.mod` module `github.com/kyleaupton/flashit`); `golift.io/udf`
  needs it
- Wails v3.0.0-beta.25 (`github.com/wailsapp/wails/v3`, `@wailsio/runtime`)
- Vue 3 + TypeScript 5.9 + Vite 7, Pinia, Tailwind 4, reka-ui/shadcn-vue
- Task (`Taskfile.yml`) drives build and dev

WIM reading, splitting and LZX decompression are pure Go in `internal/wim`;
there is no wimlib dependency. Windows ISOs are read in process through
`golift.io/udf`, pinned at v0.1.0 and imported only by `internal/isofs`
(decision 007). cgo is used only on macOS: the helper's
Authorization/DiskArbitration bindings in `internal/helper/*_darwin.{c,h}`
and the app's AuthorizationRef shim in `internal/priv/authz_darwin.{c,h}`.

## Layout

```
main.go, version.go          Wails app entry point, service registration
internal/core/               Shared contracts: Plan, Runnable, Event, Installer
internal/pipeline/           Generic typed pipeline (Step[C], cleanup on failure)
internal/jobs/               Job manager: enqueue (one job at a time), run, cancel
internal/service/            Wails services: JobsService, DrivesService, SourcesService, PrivService
internal/sources/            Pure-Go image probe: ISO 9660 PVD and directory tree (Joliet first), UDF tree, MBR signature; derives the source kind
internal/installers/linux/   Linux installer + its steps/
internal/installers/windows/ Windows installer + its steps/
internal/drives/             Removable drive enumeration per OS (+ mock provider)
internal/iso/                ISO mounting per OS
internal/isofs/              In-process UDF reader for Windows ISOs (fs.FS), the only golift importer; isocompare/ checks it against the host mount
internal/wim/                WIM reader, splitter, lzx/ decompressor
internal/fs/                 File copy from an fs.FS onto the target volume
internal/proto/              Wire types shared by the app and the Go helper (NDJSON, protocol 4)
internal/helper/             Helper server: validate/, ops, one-op-at-a-time; disk_linux.go + auth_linux.go are the Linux bindings, {disk,authz,authopen}_darwin.go + diskutil.go the macOS ones, helpertest/ holds fakes
internal/priv/               Privileged service clients: client.go (shared protocol client), transport_linux.go + service_linux.go (pkexec), transport_darwin.go + service_darwin.go + authz_darwin.c (child helper, authopen), keepalive.go (HoldDuring), privtest/ (fake service), windows/ (old helper)
internal/update/             Wails updater setup: mode per platform, guard on the endpoint provider, restart gated on jobs
internal/eventbus/           Global emitter wired to app.Event.Emit
internal/logger/             slog wrapper backed by the Wails logger
cmd/flashit-helper/          Go helper entry point: serve_linux.go (pkexec, one session) and serve_darwin.go (child of the app, serves fd 3)
cmd/wimtest/                 CLI for exercising the WIM splitter
cmd/isotest/                 CLI comparing isofs with the host mount, per file size and SHA-256
helpers/windows/             Old C privileged helper for Windows, still shipped
frontend/src/                Vue app; frontend/bindings/ is generated and committed
build/                       Per-platform Taskfiles and packaging config; build/linux holds nfpm.yaml, the polkit policy, the desktop file, icons and smoke-test.sh
build/updater/               updater.key.pub (the update trust root), harness.sh, check-release-binary.sh
updater_release.go, updater_harness.go  Update source per build tag (updatertest is the harness)
.github/workflows/           ci.yml; package-{macos,windows,linux}.yml; release.yml
docs/handovers, docs/spikes  Delegated work briefs and spike write-ups
```

## Commands

```bash
task dev                  # hot-reload dev build; on macOS assembles and signs bin/FlashIt.dev.app with the helper inside (see DEV_SETUP.md)
task build                # build for the host OS into bin/
task darwin:package       # release bundle bin/FlashIt.app (APPLE_SIGNING_IDENTITY, or ad hoc)
task darwin:updater:archive VERSION=x.y.z   # bin/flashit-<ver>-darwin-<arch>.tar.gz, the updater archive
build/updater/harness.sh build-macos|build-linux|serve|run-macos|run-linux   # local updater test, see DEV_SETUP.md
task linux:package        # on Linux, for the host arch: bin/flashit_<ver>_<arch>.deb and bin/flashit-<ver>-1.<rpmarch>.rpm; VERSION=1.2.3 overrides git describe
sudo build/linux/smoke-test.sh bin/flashit_*.deb [ver]   # install, check layout and polkit action, purge; CI runs the same script (rpm in fedora:latest)
gh workflow run package-linux.yml --ref <branch> [-f version=1.2.3]   # amd64 + arm64 packages, smoke-tested, as artifacts linux-amd64, linux-arm64, linux-sha256sums
go test -race ./...       # tests live in internal/proto, internal/helper, internal/priv, internal/pipeline, internal/sources, internal/jobs, internal/isofs, internal/fs, internal/wim, internal/installers/windows, internal/installers/windows/steps, internal/installers/linux/steps, internal/drives (linux-only)
go run ./cmd/isotest <iso>...   # isofs vs hdiutil (macOS) or mount -o loop,ro (Linux, root); exits non-zero on any difference. Run on every Windows ISO at hand before bumping golift.io/udf
go test -run XXX -fuzz FuzzOpen -fuzztime 10m ./internal/isofs
FLASHIT_ISO_CORPUS=<dir> go test -run Corpus ./internal/isofs          # isotest over every .iso in <dir>
FLASHIT_WIN_ISO=<iso> go test -run SplitFromISO ./internal/wim         # .swm parts from isofs and from the mount are identical; needs the WIM's size in TMPDIR
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
| Linux   | yes (`lsblk`) | not needed, read in process (`internal/isofs`) | `cmd/flashit-helper` spawned via `/usr/bin/pkexec`, unix socket |
| Windows | yes (PowerShell) | yes | named-pipe helper |

Linux ships as `.deb` and `.rpm` only (no AppImage, Flatpak or Snap: none
can install a root-owned helper or a polkit policy), amd64 and arm64, on the
GTK4 + WebKitGTK 6 stack: Ubuntu 24.04+, Debian 13+, Fedora 40+. Installed:

| What | Where |
| --- | --- |
| App | `/usr/bin/flashit` |
| Helper | `/usr/libexec/flashit/flashit-helper`, root:root 0755 |
| Polkit action `dev.kyleupton.flashit.helper` | `/usr/share/polkit-1/actions/dev.kyleupton.flashit.policy`, `auth_admin` everywhere |
| Desktop file | `/usr/share/applications/dev.kyleupton.flashit.desktop` |
| Icons | `/usr/share/icons/hicolor/<n>x<n>/apps/flashit.png`, 16 to 512 |

The GTK application id is `dev.kyleupton.flashit`, which is also the Wayland
app_id that matches the window to the desktop file. The version comes from
the tag without the `v` (`-X main.Version=` on the app and the helper, and
the package version); untagged builds say `dev` or `0.0.0-<commit>`.
`release.yml` calls `package-linux.yml` and attaches its packages and
`SHA256SUMS`.

Installer support, derived from `Plan` guards:

| Installer  | macOS | Linux | Windows |
| ---------- | ----- | ----- | ------- |
| Linux ISO  | yes   | yes   | yes     |
| Windows ISO| yes   | yes   | yes     |

The Windows installer's first step, `OpenSource`, reads the ISO in process
through `internal/isofs` on Linux (where `iso.IsMountSupported()` is false)
and on macOS or Windows when `FLASHIT_ISO_READER=go`; otherwise it mounts
the ISO and reads it through `os.DirFS`. The rest of the pipeline sees an
`fs.FS` either way. On Linux the helper's format mounts the volume as root
under `/run/media/flashit/<label>`, so `FormatUSB.Cleanup` (on failure or
cancel) and `Finalize` unmount and eject it through the helper
(`FlashContext.HelperMounts`); macOS and Windows eject through `drives` as
before.

## Releases

Each `package-<os>.yml` builds and tests one platform, runs by hand
(`workflow_dispatch`) or from `release.yml` (`workflow_call`), and uploads
workflow artifacts (14 days). `package-macos.yml` takes `signing`: `adhoc`
by default when run by hand, `developer-id` from the release, which signs,
notarizes and staples and fails at once if an Apple secret is missing. It
never falls back to ad hoc. A `v*` tag runs `release.yml`: resolve the
version, call the three, sign `manifest.json` over the two macOS updater
archives and one deb per Linux arch, verify it against
`build/updater/updater.key.pub`, check it holds exactly darwin/arm64,
darwin/amd64, linux/arm64 and linux/amd64, then publish everything with
one `SHA256SUMS`. A version with a `-suffix` publishes as a prerelease,
which `releases/latest` (and so the updater) skips. Run by hand,
`release.yml` is a dry run: no publish, manifest signed with a key made for
the run.

## Updater

Two unrelated things are both called helpers. The **updater** is the Wails
updater (`pkg/updater`), which swaps the app by relaunching it in its own
helper mode; the **privileged helper** is `cmd/flashit-helper`. Say
"updater" and "privileged helper" in code, docs and UI.

- macOS: full update. Checks
  `releases/latest/download/manifest.json` 10 s after start and every
  6 h, downloads `flashit-<ver>-darwin-<arch>.tar.gz`, verifies, unpacks
  `FlashIt.app` and offers "Restart to update". The privileged helper is
  inside the bundle and updates with it. A copy the updater could not
  swap (run from the DMG, translocated, or in a folder the user cannot
  write) gets the Linux notice instead.
- Linux: notify only, "FlashIt X is available" and "View release". It
  never downloads: `/usr/bin` is the package manager's.
- Windows and any `Version` that is not a plain `X.Y.Z` (`dev`, git
  describe, `0.0.0-dev.N`): off.

`internal/update` never sets `CheckInterval` or calls `CheckAndInstall`:
that path downloads on every platform and listens for restart events any
page can emit. The manifest is unsigned; only each artifact's bytes are.
So the guard provider refuses any artifact that is not ed25519ph-signed,
any version that is not `X.Y.Z`, a darwin artifact under any name or URL
but `flashit-<ver>-darwin-<arch>.tar.gz` in that version's release, and
more bytes than the manifest's size (512 MiB cap). Before offering the
restart, `CheckBundle` requires the unpacked bundle to be `FlashIt.app`,
`dev.kyleupton.flashit`, at the manifest's version (a signed old release
replayed as new fails here), pass `codesign --verify --deep --strict`, and
carry the running app's Team ID when it is team-signed (an unreadable
signature refuses). A failed restart discards the staged bundle, so the
next check downloads again. Never restart during a
job: `UpdateService.Restart` takes `JobGate`, which refuses while a job is
pending, running or being planned, and blocks new jobs from then on.

Key custody: `build/updater/updater.key.pub` is the only trust root and is
committed. The private key is the Actions secret `UPDATER_PRIVATE_KEY`
(and Kyle's password manager); only the release `manifest` job reads it,
from a 0600 file deleted in an `always()` step. Never create, read or
print it. The `updatertest` build tag swaps in a throwaway key from
`bin/updatertest/key/` and honours `FLASHIT_UPDATE_URL`;
`check-release-binary.sh` fails every package workflow whose binary has
that string or lacks the committed key.

## Job execution flow

The source kind is probed, never chosen by the user (decision 005).
`SourcesService.Probe` runs when a file is picked or dropped so the UI can
show the label, size and kind, or the reason an image is unusable.
`JobsService.StartJob(SourcePath, DriveID)` probes again, refuses an
`Unknown` image with its reason, picks the installer by kind, resolves the
drive from `drives.ListRemovable` (refusing one not listed) and calls
`installer.Plan(ctx, SourceInfo, Drive)`, which returns a `core.Plan`
wrapping a bound `pipeline.Pipeline`. `jobs.Manager.Enqueue` refuses with
`ErrJobActive` while a job is pending or running, otherwise stores the job
and runs it on a cancellable background context. Steps emit through
`core.Executor`, the manager forwards to `eventbus.Emit("job:event", ev)`,
and Wails delivers it to the frontend, where `frontend/src/stores/job.ts`
subscribes with `Events.On('job:event', ...)`.

Both installers wrap their pipeline in `priv.HoldDuring`, so the job's
`Run`, cleanups included, holds the privileged session. On Linux the hold
pings the helper every 20 s (a third of its 60 s idle timeout) so a long
copy cannot let it exit before the eject; the hold ends when `Run` returns,
whatever the outcome, and on `Shutdown`, and the helper idles out about 60 s
later. A failed ping only stops the keepalive: it never respawns the helper
or raises a second polkit prompt. macOS and Windows need no hold.

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

The backend emits `Type` values `state`, `step-start`, `step-end`, `progress`,
`log`, `authorizing` and `warning`; a failure arrives as a `state` event with `Error`
set and `Code` when the helper refused. `authorizing` is emitted by the write
and format steps right before the privileged call that raises the OS prompt,
and the frontend clears it on the next `progress` or `step-end`. A pipeline
step that implements `CleanupStep` is cleaned up in reverse order when a
later step fails; the failing step cleans up after itself (the WIM split
removes the `.swm` parts it wrote to the volume). `warning` carries text in
`Message` the user must act on although the job succeeded: an eject that
the helper answered `device_busy` (the files are written but something
holds the stick), or on Linux any failed eject of the volume the helper
mounted. The job store collects warnings per job, and a succeeded
job with warnings gets `toast.warning` and a warning alert in the done
view.

Wails only delivers `WindowFilesDropped` to Go listeners, so `main.go`
relays dropped paths to the frontend as a `files:dropped` event.

The frontend state machine lives in `frontend/src/stores/app.ts`:
`idle -> source-probed -> target-selected -> running -> done | failed |
cancelled`, with `authorizing` as a substate of `running` on the job store.

## Safety rules

`JobsService.StartJob` requires the device to appear in
`drives.ListRemovable`. Both installers refuse a target whose ID ends in
`disk0` and an image larger than the drive, in `Plan`:
`internal/installers/linux/linux.go` and
`internal/installers/windows/windows.go`. The `disk0` check and the
privileged-service setup are skipped when `core.DryRun` is set (`DRY_RUN=1`),
which also swaps in `drives.MockProvider` and lets any readable file, even
one that probes as `Unknown`, run the Linux installer's simulated pipeline.

A Windows ISO read in process is untrusted input that decides every path
written to the stick, so `isofs.Open` checks the whole tree before the
pipeline formats anything (decision 007). It reads the UDF volume layout
itself first and accepts one type 1 partition only, then walks golift's
tree and rejects: names that are empty, `.` or `..`, not valid UTF-8,
longer than 255 UTF-16 units, hold `/ \ < > : " | ? *` or control
characters, end in a dot or space, or name a Windows device (`CON`,
`nul.txt`, `COM1`); two names in one directory that differ only by case;
any UDF file type but a regular file or directory (symlinks, devices);
the same file entry reached twice (cycles, hard links); more than 32
levels or 100,000 entries; chained allocation descriptors, which golift
follows without a bound; more than 64 MiB of directory and file entry
metadata or a million extents; and a file whose extents do not all read
back up to its recorded length. golift panics are recovered into errors.
`fs.CopyDir` then refuses any destination that `filepath.IsLocal` rejects
or that lands outside the volume, anything but a regular file (symlinks on
a mounted image), a file that yields a different byte count than its
size, and overwriting an existing file.

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

Linux, before any of that: a packaged build (`-tags production`) spawns only
`/usr/libexec/flashit/flashit-helper`, and refuses unless the helper is a
regular file, not a symlink, and it and every directory up to `/` are real
directories owned by root with no group or other write bit
(`internal/priv/rootowned_unix.go`). That keeps a process running as the
user from getting a user-writable binary run as root under FlashIt's polkit
message. Dev builds still look next to the executable and in `helpers/`.

Linux: removable means sysfs `removable` or a USB ancestor; system disks are
resolved from `/`, `/boot`, `/home` and friends through dm/md slaves (btrfs
and ZFS roots through the mount source), fail closed. The caller's
`SO_PEERCRED` uid must equal `PKEXEC_UID`; the socket lives in a 0700
directory the app creates. `eject` and `unmount` by device only act on a
target that passed the same gate as a write, and unmount every partition
and the disk (mounts matched by major:minor from `/proc/self/mountinfo`),
trying five times a second apart on EBUSY before answering `device_busy`.
They then remove the directories that device was mounted on, found in the
mount table before unmounting and never taken from the request: only
`MountRoot/<valid label>`, only if `Lstat` shows a real directory (not a
symlink) on the same filesystem as `MountRoot` (no longer a mountpoint), and
only by `rmdir`. `eject` then syncs and, if `eject(1)` is installed,
re-reads the partition table and runs it (without it the re-read is skipped,
since it can make the desktop automount the stick again); once the unmount
worked, a failure there is only logged.

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
