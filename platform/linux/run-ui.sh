#!/bin/sh
# Run the dashboard in the foreground without systemd (testing / development).
#   ./run-ui.sh                 state in ./state, port 18741
#   STATE_DIR=/path PORT=18750 NATIVE=/data/appleservices/native ./run-ui.sh
# Stop with Ctrl+C. Listens on 127.0.0.1 only; use an SSH tunnel from another
# machine (see README-linux.md). Keep STATE_DIR / TMPDIR on the data disk.
set -eu
HERE=$(cd "$(dirname "$0")" && pwd)
STATE_DIR=${STATE_DIR:-$HERE/state}
PORT=${PORT:-18741}
NATIVE=${NATIVE:-$HERE/native}
for binary in dashboard trusted-device accountsetupcheck nacexchange familyinspect; do
	[ -x "$HERE/bin/$binary" ] || { echo "missing $HERE/bin/$binary; build with scripts/build-linux.sh" >&2; exit 1; }
done
if [ ! -f "$NATIVE/librust_lib_bluebubbles.so" ]; then
	echo "missing $NATIVE/librust_lib_bluebubbles.so; run ./setup-native.sh $NATIVE" >&2
	exit 1
fi
for path in "$HERE/.cache/mac-nac-runtime/emulated/IMDAppleServices" "$HERE/.cache/activation-source-review/mac-io-encrypt.dll"; do
	[ -f "$path" ] || { echo "missing Linux NAC runtime: $path" >&2; exit 1; }
done
mkdir -p "$STATE_DIR"
chmod 0700 "$STATE_DIR"
cd "$HERE"
# The dashboard must not inherit ambient login credentials or preloads.
exec env -u APPLESERVICES_APPLEID -u APPLESERVICES_PASSWORD -u LD_PRELOAD \
	"$HERE/bin/dashboard" -dir "$STATE_DIR" -worker "$HERE/bin/trusted-device" \
	-adi-dll "$NATIVE/librust_lib_bluebubbles.so" -mac-push-setup "$HERE/tools/mac-push-check.py" \
	-python "$(command -v python3)" -port "$PORT" "$@"
