# Development setup (macOS)

FlashIt writes disks through `flashit-helper`, an unprivileged child process
the app spawns from inside its bundle. Nothing runs as root: for each flash
the helper asks `/usr/libexec/authopen` for the open raw device after the
authorization sheet, and macOS gates that behind the Removable Volumes
permission, which it prompts for once per app identity. A dev build needs
a stable code signing identity so that grant survives rebuilds.

## Clean slate from the daemon builds

Earlier builds of this branch registered a root launchd daemon and an
authorization right on this machine. Remove them once; nothing in the
current build needs or checks them.

```bash
sudo launchctl bootout system/dev.kyleupton.flashit.helper
sudo rm -f /var/run/dev.kyleupton.flashit.sock /var/log/dev.kyleupton.flashit.helper.log
sudo security authorizationdb remove dev.kyleupton.flashit.write
sudo pkill -f flashit-helper
rm -rf bin/FlashIt.dev.app
```

Then check Login Items & Extensions › Allow in the Background. If FlashIt
is still listed there after the commands above, `sfltool resetbtm` clears
it, at the cost of every app's background item, so only do that if the
entry bothers you.

### Machines that ran the SMJobBless helper

Builds before the Go helper installed a daemon under the same label from
`/Library`:

```bash
sudo launchctl bootout system/dev.kyleupton.flashit.helper
sudo rm -f /Library/LaunchDaemons/dev.kyleupton.flashit.helper.plist \
           /Library/PrivilegedHelperTools/dev.kyleupton.flashit.helper
```

## One-time: create the dev certificate

1. Open **Keychain Access** (Applications › Utilities).
2. **Keychain Access › Certificate Assistant › Create a Certificate…**
3. Fill in:
   - **Name**: `FlashIt Dev Code Signing` (exactly this)
   - **Identity Type**: Self Signed Root
   - **Certificate Type**: **Code Signing** (not "Root Certificate")
4. Create, Continue, Done. It lands in the login keychain, valid for a year.

Without it `task dev` signs ad hoc, which works, but an ad-hoc signature
is a new identity on every build, so macOS asks for Removable Volumes
access again after each rebuild.

## Daily workflow

```bash
task dev
```

This builds the app and the helper, assembles `bin/FlashIt.dev.app` with the
helper at `Contents/MacOS/flashit-helper`, signs both with the dev
certificate and the hardened runtime, and launches the app through `open`
so macOS attributes the Removable Volumes prompt to the bundle rather than
to Terminal. The app's log, including the helper's lines prefixed
`helper:`, goes to `bin/FlashIt.dev.log`.

The first flash on a fresh machine shows two prompts: `"FlashIt" would like
to access files on a removable volume` (Allow), then the password sheet
`FlashIt wants to make changes.` raised by the helper right before the
write. From then on it is one sheet per flash. Clicking Don't Allow on the
first prompt makes the flash fail with a "Permission needed" panel that
opens Privacy & Security › Files and Folders › Removable Volumes; switch
FlashIt on there and try again.

To see the prompt again:

```bash
tccutil reset SystemPolicyRemovableVolumes dev.kyleupton.flashit
```

## Watching the helper

```bash
tail -f bin/FlashIt.dev.log
```

While a write runs, `lsof /dev/rdiskN` lists only `flashit-helper`; authd's
view of the sheet is in `log stream --predicate 'process == "authd"'`.

## Release builds

`task darwin:package` builds `bin/FlashIt.app` signed with
`APPLE_SIGNING_IDENTITY` when set and ad hoc otherwise, and
`task darwin:updater:archive VERSION=x.y.z` packs it for the updater. The
release workflow notarizes and staples the bundle before packing it.

## Testing the updater locally

`build/updater/harness.sh` builds 0.0.1 and 0.0.2 with the `updatertest`
tag, which embeds a throwaway key from `bin/updatertest/key/` (made fresh by
every build) and reads the manifest from `FLASHIT_UPDATE_URL`, default
`http://127.0.0.1:8765/manifest.json`. The real key is never involved.

```bash
build/updater/harness.sh build-macos          # ad hoc 0.0.1 and 0.0.2, ~2 min
build/updater/harness.sh serve                # terminal 1: signs and serves 0.0.2
build/updater/harness.sh run-macos            # terminal 2: a fresh copy of 0.0.1
tail -f "$TMPDIR/flashit-updatertest.log"     # every state, across the relaunch
```

About 10 s after launch the pill reads "FlashIt 0.0.2 is ready — Restart
to update"; the menu's FlashIt › Check for Updates… checks at once. Restart
swaps `bin/updatertest/app/FlashIt.app` and relaunches 0.0.2, which then
reports up to date. The swap log is `$TMPDIR/wails-update-<pid>.log`.
`run-macos --auto-restart` restarts as soon as the update is ready.

`serve tampered` flips one byte of the archive after signing and
`serve wrong-key` signs with another key; either way 0.0.1 logs an error
and keeps running. `DRY_RUN=1 build/updater/harness.sh run-macos` swaps in
mock drives, so a simulated flash shows Restart disabled while it runs.

On Ubuntu, `harness.sh build-linux`, `serve`, then `run-linux` installs the
0.0.1 deb (sudo) and starts it; the pill offers "View release", and the
server log shows only the manifest being fetched.
