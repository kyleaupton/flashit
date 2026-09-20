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
certificate and the hardened runtime, and launches the app.

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

The helper's log goes to the app's log, prefixed `helper:`. While a write
runs, `lsof /dev/rdiskN` lists only `flashit-helper`; authd's view of the
sheet is in `log stream --predicate 'process == "authd"'`.

## Release builds

`task darwin:package` builds `bin/FlashIt.app` signed with
`APPLE_SIGNING_IDENTITY` when set and ad hoc otherwise; `APPLE_TEAM_ID` is
needed for the Info.plist. The release workflow then notarizes the bundle.
