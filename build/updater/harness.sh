#!/usr/bin/env bash
# Local end-to-end test of the updater with a throwaway key: builds FlashIt
# 0.0.1 and 0.0.2 with the updatertest tag, signs a manifest for 0.0.2 and
# serves it on 127.0.0.1:8765. See DEV_SETUP.md.
#
#   harness.sh build-macos | build-linux     both versions, a fresh test key
#   harness.sh serve [good|tampered|wrong-key]
#   harness.sh run-macos [--auto-restart]    launch 0.0.1
#   harness.sh run-linux                     install the 0.0.1 deb, launch it
#
# The real key is never involved: builds with this tag embed only
# bin/updatertest/key/updater-test.key.pub.
set -euo pipefail

cd "$(dirname "$0")/../.."
OUT=bin/updatertest
KEY=$OUT/key/updater-test.key
PORT=8765
URL=http://127.0.0.1:$PORT
OLD=0.0.1
NEW=0.0.2

goarch() { go env GOARCH; }

newkey() {
	mkdir -p "$OUT/key"
	wails3 updater genkey -out "$KEY" -force >/dev/null
	echo "test key: $KEY.pub"
}

build_macos() {
	newkey
	rm -rf "$OUT/app" "$OUT/old" "$OUT/dist"
	mkdir -p "$OUT/old" "$OUT/dist"

	task darwin:package VERSION=$OLD EXTRA_TAGS=updatertest
	ditto bin/FlashIt.app "$OUT/old/FlashIt.app"

	task darwin:package VERSION=$NEW EXTRA_TAGS=updatertest
	task darwin:updater:archive VERSION=$NEW
	mv "bin/flashit-$NEW-darwin-$(goarch).tar.gz" "$OUT/dist/"
	ls -l "$OUT/dist"
}

build_linux() {
	newkey
	rm -rf "$OUT/dist" "$OUT/old"
	mkdir -p "$OUT/dist" "$OUT/old"

	task linux:package VERSION=$OLD EXTRA_TAGS=updatertest
	mv bin/flashit_${OLD}_*.deb "$OUT/old/"
	rm -f bin/flashit-${OLD}-*.rpm

	task linux:package VERSION=$NEW EXTRA_TAGS=updatertest
	mv bin/flashit_${NEW}_*.deb bin/flashit-${NEW}-*.rpm "$OUT/dist/"
	ls -l "$OUT/old" "$OUT/dist"
}

serve() {
	local variant=${1:-good} key=$KEY
	[ -f "$KEY" ] || { echo "run build-macos or build-linux first" >&2; exit 1; }
	rm -rf "$OUT/serve"
	mkdir -p "$OUT/serve"
	shopt -s nullglob
	# Only the updater archives and debs go in, as in the release workflow.
	local files=("$OUT"/dist/*.tar.gz "$OUT"/dist/*.deb)
	cp "${files[@]}" "$OUT/serve/"

	if [ "$variant" = wrong-key ]; then
		key=$OUT/key/other.key
		wails3 updater genkey -out "$key" -force >/dev/null
	fi
	wails3 updater manifest -version $NEW -notes "Harness build $NEW" -key "$key" \
		-url-prefix "$URL" -output "$OUT/serve/manifest.json" "$OUT/serve"

	if [ "$variant" = tampered ]; then
		# One byte changed after signing.
		for f in "$OUT"/serve/*.tar.gz "$OUT"/serve/*.deb; do
			python3 - "$f" <<'EOF'
import sys
p = sys.argv[1]
b = bytearray(open(p, 'rb').read())
b[len(b) // 2] ^= 0xFF
open(p, 'wb').write(b)
EOF
		done
	fi

	echo "--- manifest ($variant)"
	cat "$OUT/serve/manifest.json"
	echo "--- verify against the test key"
	wails3 updater verify -manifest "$OUT/serve/manifest.json" -publickey "$KEY.pub" || true
	echo "--- serving $URL/manifest.json (Ctrl-C to stop)"
	exec python3 -m http.server $PORT --bind 127.0.0.1 --directory "$OUT/serve"
}

run_macos() {
	local args=(--env "FLASHIT_UPDATE_URL=$URL/manifest.json")
	[ "${1:-}" = --auto-restart ] && args+=(--env FLASHIT_UPDATE_AUTORESTART=1)
	pkill -f "$OUT/app/FlashIt.app/Contents/MacOS/FlashIt" 2>/dev/null || true
	sleep 1
	# Every run starts from a fresh copy of 0.0.1; an update replaces it.
	rm -rf "$OUT/app"
	mkdir -p "$OUT/app"
	ditto "$OUT/old/FlashIt.app" "$OUT/app/FlashIt.app"
	echo "log: ${TMPDIR:-/tmp}/flashit-updatertest.log"
	open "${args[@]}" --stdout "$OUT/app.log" --stderr "$OUT/app.log" "$OUT/app/FlashIt.app"
}

run_linux() {
	sudo apt-get install -y --allow-downgrades "./$(ls "$OUT"/old/flashit_${OLD}_*.deb)"
	FLASHIT_UPDATE_URL=$URL/manifest.json flashit
}

case "${1:-}" in
build-macos) build_macos ;;
build-linux) build_linux ;;
serve) serve "${2:-good}" ;;
run-macos) run_macos "${2:-}" ;;
run-linux) run_linux ;;
*)
	sed -n '2,13p' "$0"
	exit 2
	;;
esac
