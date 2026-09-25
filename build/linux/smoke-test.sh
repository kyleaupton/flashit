#!/bin/sh
# Installs a FlashIt .deb or .rpm, checks the installed layout and the polkit
# action, removes it and checks nothing is left. Run as root on a disposable
# system: CI runs it on the runner (deb) and in fedora:latest (rpm).
#
#   smoke-test.sh <package> [version]
#
# With a version, the helper must report exactly that version.
set -eu

pkg=$1
version=${2:-}

fail() {
	echo "FAIL: $*" >&2
	exit 1
}

[ "$(id -u)" = 0 ] || fail "run as root"
[ -f "$pkg" ] || fail "$pkg not found"

case $pkg in
*.deb)
	export DEBIAN_FRONTEND=noninteractive
	apt-get update -qq
	apt-get install -y -qq "./${pkg#./}"
	;;
*.rpm)
	dnf install -y -q "$pkg"
	;;
*) fail "$pkg is neither a .deb nor an .rpm" ;;
esac

# path, then "owner:group mode type" as stat prints it
expected="
/usr/bin/flashit|root:root 755 regular file
/usr/libexec/flashit|root:root 755 directory
/usr/libexec/flashit/flashit-helper|root:root 755 regular file
/usr/share/polkit-1/actions/dev.kyleupton.flashit.policy|root:root 644 regular file
/usr/share/applications/dev.kyleupton.flashit.desktop|root:root 644 regular file"
for size in 16 24 32 48 64 128 256 512; do
	expected="$expected
/usr/share/icons/hicolor/${size}x${size}/apps/flashit.png|root:root 644 regular file"
done

echo "$expected" | while IFS='|' read -r path want; do
	[ -n "$path" ] || continue
	got=$(stat -c '%U:%G %a %F' "$path" 2>/dev/null) || fail "$path is missing"
	[ "$got" = "$want" ] || fail "$path is '$got', want '$want'"
	echo "ok   $path ($got)"
done

if command -v desktop-file-validate >/dev/null 2>&1; then
	desktop-file-validate /usr/share/applications/dev.kyleupton.flashit.desktop
	echo "ok   desktop file validates"
fi

# pkaction asks polkitd over the system bus. A container has neither, so
# start both for the check.
if [ ! -S /run/dbus/system_bus_socket ]; then
	if ! command -v dbus-daemon >/dev/null 2>&1; then
		case $pkg in
		*.deb) apt-get install -y -qq dbus-daemon ;;
		*.rpm) dnf install -y -q dbus-daemon ;;
		esac
	fi
	mkdir -p /run/dbus
	dbus-daemon --system --fork
	/usr/lib/polkit-1/polkitd --no-debug >/dev/null 2>&1 &
	sleep 2
fi
action=$(pkaction --action-id dev.kyleupton.flashit.helper --verbose) ||
	fail "pkaction does not know dev.kyleupton.flashit.helper"
echo "$action"
echo "$action" | grep -q 'org.freedesktop.policykit.exec.path -> /usr/libexec/flashit/flashit-helper' ||
	fail "policy exec.path is not the helper"
echo "$action" | grep -q 'Authentication is required to let FlashIt write to a USB drive' ||
	fail "policy message missing"
for scope in any inactive active; do
	echo "$action" | grep -Eq "implicit $scope: +auth_admin$" || fail "implicit $scope is not auth_admin"
done
echo "ok   polkit action"

# Without PKEXEC_UID the helper refuses to serve, but it logs its version first.
out=$(env -u PKEXEC_UID /usr/libexec/flashit/flashit-helper -socket /nonexistent/helper.sock 2>&1) &&
	fail "helper served without PKEXEC_UID"
echo "$out"
if [ -n "$version" ]; then
	echo "$out" | grep -q "helper=$version " || fail "helper does not report version $version"
	echo "ok   helper reports $version"
fi

case $pkg in
*.deb) apt-get remove --purge -y -qq flashit ;;
*.rpm) dnf remove -y -q flashit ;;
esac

echo "$expected" | while IFS='|' read -r path want; do
	[ -n "$path" ] || continue
	if [ -e "$path" ] || [ -L "$path" ]; then
		fail "$path is left after removal"
	fi
done
echo "ok   removal leaves nothing behind"
echo PASS
