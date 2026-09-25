#!/usr/bin/env bash
# Fails unless each binary embeds the committed updater public key and was
# not built with the updatertest tag, whose throwaway key and
# FLASHIT_UPDATE_URL override must never ship.
set -euo pipefail

pub="$(dirname "$0")/updater.key.pub"
key=$(sed -n 2p "$pub")
[ -n "$key" ] || { echo "::error::no key in $pub"; exit 1; }

status=0
for bin in "$@"; do
	if grep -aqF FLASHIT_UPDATE_URL "$bin"; then
		echo "::error::$bin is an updatertest build"
		status=1
	elif ! grep -aqF "$key" "$bin"; then
		echo "::error::$bin does not embed build/updater/updater.key.pub"
		status=1
	else
		echo "$bin: release updater key, no test hooks"
	fi
done
exit $status
