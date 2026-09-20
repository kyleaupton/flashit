# Development setup (macOS)

FlashIt does its disk work through a privileged helper. On macOS that is a
launchd daemon inside the app bundle, registered with `SMAppService` on
first use. The daemon only talks to processes signed as `dev.kyleupton.flashit`
with a known certificate, so a dev build needs a code signing identity.

## One-time: create the dev certificate

1. Open **Keychain Access** (Applications › Utilities).
2. **Keychain Access › Certificate Assistant › Create a Certificate…**
3. Fill in:
   - **Name**: `FlashIt Dev Code Signing` (exactly this)
   - **Identity Type**: Self Signed Root
   - **Certificate Type**: **Code Signing** (not "Root Certificate")
4. Create, Continue, Done. It lands in the login keychain, valid for a year.

Check it:

```bash
security find-certificate -c "FlashIt Dev Code Signing" -Z | grep SHA-1
```

The hash printed is what the helper is built to accept. The identity does
not appear in `security find-identity -p codesigning`; that is normal for a
self-signed certificate and does not matter.

If the details window says "Self-signed root certificate", delete it (and its
private key) and create it again with **Code Signing** selected.

## Daily workflow

```bash
task dev
```

This builds the app and the helper, assembles `bin/FlashIt.dev.app` with the
helper and its launchd plist inside, signs everything with the dev
certificate, and launches the app. Without the certificate the bundle is
signed ad hoc, a warning is printed, and the helper refuses the app.

The first time a flash starts, the app registers the daemon and macOS shows
"Background Items Added". The flash fails with a "Permission needed" panel:
click **Open System Settings**, switch **FlashIt** on under Login Items &
Extensions › Allow in the Background, then **Try again**. From then on the
daemon runs as root under launchd and every launch of the app finds it.

Each flash raises exactly one authorization sheet ("FlashIt needs to write to
a removable drive."), raised by the helper right before the destructive step.

When the helper's sources change, `task dev` bakes a new version stamp into
it; the app notices on its next connection, unregisters and registers the
daemon again. Registering can keep failing for about a minute after an
unregister; the app retries on its own. No System Settings visit is needed.

## Watching the helper

```bash
tail -f /var/log/dev.kyleupton.flashit.helper.log
launchctl print system/dev.kyleupton.flashit.helper | grep -E 'state|runs|last exit'
security authorizationdb read dev.kyleupton.flashit.write
```

## Clean slate

Unregistering leaves the socket and the authorization right behind; they
are ours to remove. The Background Items record stays (only
`sfltool resetbtm` clears those, for every app, so leave it).

```bash
sudo launchctl bootout system/dev.kyleupton.flashit.helper
sudo security authorizationdb remove dev.kyleupton.flashit.write
sudo pkill -f flashit-helper
sudo rm -f /var/run/dev.kyleupton.flashit.sock /var/log/dev.kyleupton.flashit.helper.log
rm -rf bin/FlashIt.dev.app
```

### Machines that ran the old helper

Builds before the Go helper installed an SMJobBless daemon under the same
label. `SMAppService` reports it as enabled and the app refuses to proceed
until it is gone:

```bash
sudo launchctl bootout system/dev.kyleupton.flashit.helper
sudo rm -f /Library/LaunchDaemons/dev.kyleupton.flashit.helper.plist \
           /Library/PrivilegedHelperTools/dev.kyleupton.flashit.helper
```

## Release builds

`task darwin:package` builds `bin/FlashIt.app` signed with
`APPLE_SIGNING_IDENTITY` (ad hoc when unset) and bakes `APPLE_TEAM_ID` into
the helper, so it accepts any FlashIt signed by that team. The release
workflow then notarizes the bundle.
