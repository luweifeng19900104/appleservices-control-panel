#!/bin/sh
# Build the Linux distribution into dist/linux and pack it.
#   scripts/build-linux.sh            linux/amd64
# Full native functionality currently supports linux/amd64 only.
# Output includes every dashboard helper and the verified NAC runtime.
#         dist/appleservices-linux-dist.tar.gz
set -eu
ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"
GOARCH=${GOARCH:-amd64}
[ "$GOARCH" = "amd64" ] || { echo "full Linux build requires GOARCH=amd64" >&2; exit 1; }
OUT="$ROOT/dist/linux"
VERSION=${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || date -u +%Y%m%d%H%M%S)}
rm -rf "$OUT"
mkdir -p "$OUT/bin"
export GOOS=linux GOARCH CGO_ENABLED=0
export GOFLAGS="-p=1 ${GOFLAGS:-}"
while read -r name package; do
	"${GO:-go}" build -trimpath -ldflags="-s -w" -o "$OUT/bin/$name" "$package"
done <<'EOF'
dashboard ./cmd/dashboard
login ./cmd/login
trusted-device ./cmd/trusted-device
tunnelgate ./tools/tunnelgate
remote-gateway ./cmd/remote-gateway
accountsetupcheck ./tools/accountsetupcheck
nacexchange ./tools/nacexchange
familyinspect ./tools/familyinspect
nativeadicheck ./tools/nativeadicheck
reconnectvault ./tools/reconnectvault
EOF
for file in appleservices.service appleservices.env install.sh run-ui.sh setup-native.sh enable-public-gateway.sh README-linux.md; do
	cp "platform/linux/$file" "$OUT/$file"
done
# Python helpers used at run time (native module itself is fetched by setup-native.sh).
mkdir -p "$OUT/tools" "$OUT/nativeadi" "$OUT/gsa" "$OUT/.cache"
cp tools/mac-push-check.py tools/mac-nac-check.py tools/skill-store-transport.py "$OUT/tools/"
cp nativeadi/bridge.py nativeadi/helper.py "$OUT/nativeadi/"
cp gsa/apple_root_ca.go gsa/apple_root_ca_g3.pem "$OUT/gsa/"
cp -a platform/linux/runtime/.cache/. "$OUT/.cache/"
find "$OUT/.cache" -type d -exec chmod 0755 {} +
find "$OUT/.cache" -type f -exec chmod 0644 {} +
find "$OUT" -type d -name __pycache__ -prune -exec rm -rf {} +
find "$OUT" -type f -name '*.pyc' -delete
for file in LICENSE THIRD_PARTY_NOTICES.md DISCLAIMER.md; do
	cp "$file" "$OUT/$file"
done
cp platform/linux/reset-dashboard-password.sh "$OUT/"
chmod 0755 "$OUT/install.sh" "$OUT/run-ui.sh" "$OUT/setup-native.sh"
chmod 0755 "$OUT/enable-public-gateway.sh"
chmod 0755 "$OUT/reset-dashboard-password.sh"
printf '%s\n' "$VERSION" > "$OUT/VERSION"
(cd "$OUT" && find bin tools nativeadi gsa .cache -type f -print0 | sort -z | xargs -0 sha256sum > SHA256SUMS)
tar -C "$ROOT/dist" -czf "$ROOT/dist/appleservices-linux-dist.tar.gz" linux
echo "built $OUT ($VERSION, linux/$GOARCH)"
