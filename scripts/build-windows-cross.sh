#!/bin/sh
# Cross-build the Windows distribution from Linux/macOS (no cgo is used).
# Same result as scripts/build-windows.ps1: dist/windows in the original app\
# layout plus dist/appleservices-windows-dist.zip.
set -eu
ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"
GO=${GO:-go}
OUT="$ROOT/dist/windows"
VERSION=${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || date -u +%Y%m%d%H%M%S)}
rm -rf "$OUT" "$ROOT/dist/appleservices-windows-dist.zip"
mkdir -p "$OUT/bin"
export GOOS=windows GOARCH=amd64 CGO_ENABLED=0
"$GO" build -o "$OUT/bin/login.exe" ./cmd/login
"$GO" build -o "$OUT/bin/dashboard.exe" ./cmd/dashboard
"$GO" build -o "$OUT/bin/trusted-device-gui.exe" ./cmd/trusted-device
cp "$OUT/bin/trusted-device-gui.exe" "$OUT/bin/trusted-device.exe"
git ls-files | grep -v -e '^platform/' -e '^scripts/' -e '^Makefile$' | while IFS= read -r f; do
	mkdir -p "$OUT/$(dirname "$f")"
	cp "$f" "$OUT/$f"
done
cp platform/windows/* "$OUT/"
printf '%s\n' "$VERSION" > "$OUT/VERSION"
(cd "$OUT" && sha256sum bin/*.exe > SHA256SUMS)
if command -v zip >/dev/null 2>&1; then
	(cd "$OUT" && zip -qr ../appleservices-windows-dist.zip .)
else
	(cd "$OUT" && python3 -c 'import os,zipfile;z=zipfile.ZipFile("../appleservices-windows-dist.zip","w",zipfile.ZIP_DEFLATED)
for d,_,fs in os.walk("."):
    [z.write(os.path.join(d,f),os.path.relpath(os.path.join(d,f),".")) for f in fs]
z.close()')
fi
echo "built $OUT ($VERSION, windows/amd64)"
