#!/bin/sh
# Download and verify the pinned OpenBubbles Linux native module.
#   ./setup-native.sh [target-dir]      default /data/appleservices/native
# The module (closed ClearADI + push certificates) is licensed by OpenBubbles
# and is never shipped with this project; it is fetched from the official
# GitHub release and pinned by sha256.
set -eu
TARGET=${1:-${APPLESERVICES_ROOT:-/data/appleservices}/native}
TAG='v1.15.0+205'
URL='https://github.com/OpenBubbles/openbubbles-app/releases/download/v1.15.0%2B205/bluebubbles-linux-x86_64.tar'
TAR_SHA256=5584a4e79dbd22fcf19b51fb6ff6426d7f132555677e1ae9d3421f04657b563a
SO_SHA256=ce260415598b78c0810ae6b917145fbca804f6f19385ca9d34d55921d38210cf
SO=librust_lib_bluebubbles.so
if [ -f "$TARGET/$SO" ] && [ "$(sha256sum "$TARGET/$SO" | cut -d' ' -f1)" = "$SO_SHA256" ]; then
	echo "$TARGET/$SO already present and verified ($TAG)"
	exit 0
fi
mkdir -p "$TARGET"
WORK=$(mktemp -d "${TMPDIR:-$TARGET}/ob-XXXXXX")
trap 'rm -rf "$WORK"' EXIT
curl -fsSL -o "$WORK/ob.tar" "$URL"
echo "$TAR_SHA256  $WORK/ob.tar" | sha256sum -c -
tar -C "$WORK" -xf "$WORK/ob.tar" ./lib/$SO
echo "$SO_SHA256  $WORK/lib/$SO" | sha256sum -c -
install -m 0444 "$WORK/lib/$SO" "$TARGET/$SO"
echo "installed $TARGET/$SO ($TAG, sha256 $SO_SHA256)"
