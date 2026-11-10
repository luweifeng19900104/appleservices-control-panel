#!/bin/sh
# Install or update appleservices on Linux under systemd.
#
#   sudo ./install.sh              install this release and start the service
#   sudo ./install.sh --no-start   install, enable, but do not start
#   sudo ./install.sh --uninstall  stop and remove unit + releases (state, logs, native kept)
#
# Layout (everything on the data disk; override with APPLESERVICES_ROOT):
#   /data/appleservices/releases/<version>   program (current -> symlink)
#   /data/appleservices/native               pinned OpenBubbles Linux module (setup-native.sh)
#   /data/appleservices/state                accounts and sessions (0700)
#   /data/appleservices/logs                 dashboard log
#   /data/appleservices/tmp                  TMPDIR for the service
#   /etc/appleservices/appleservices.env     config only
#   /opt/appleservices -> /data/appleservices  optional convenience link (only if /opt/appleservices is free)
# Runs as the unprivileged system user "appleservices"; listens on 127.0.0.1 only.
set -eu

ROOT=${APPLESERVICES_ROOT:-/data/appleservices}
STATE=$ROOT/state
LOGS=$ROOT/logs
TMP=$ROOT/tmp
NATIVE=$ROOT/native
CONFIG=/etc/appleservices
UNIT=/etc/systemd/system/appleservices.service
SERVICE_USER=appleservices
HERE=$(cd "$(dirname "$0")" && pwd)

if [ "$(id -u)" -ne 0 ]; then
	echo "install.sh must run as root (sudo ./install.sh)" >&2
	exit 1
fi

if [ "${1:-}" = "--uninstall" ]; then
	systemctl disable --now appleservices.service 2>/dev/null || true
	rm -f "$UNIT"
	systemctl daemon-reload
	rm -rf "$ROOT/releases" "$ROOT/current"
	[ -L /opt/appleservices ] && [ "$(readlink /opt/appleservices)" = "$ROOT" ] && rm -f /opt/appleservices
	echo "Removed program and unit. State ($STATE), logs, native module and config ($CONFIG) kept."
	exit 0
fi

for binary in dashboard login trusted-device accountsetupcheck nacexchange familyinspect tunnelgate reconnectvault; do
	if [ ! -x "$HERE/bin/$binary" ]; then
		echo "missing $HERE/bin/$binary" >&2
		exit 1
	fi
done
if [ -f "$HERE/SHA256SUMS" ]; then
	(cd "$HERE" && sha256sum -c SHA256SUMS)
fi
for helper in python3 gcc; do
	command -v "$helper" >/dev/null 2>&1 || { echo "missing $helper (needed by the native ADI helper)" >&2; exit 1; }
done
python3 -c 'import cryptography' 2>/dev/null || { echo "missing python3-cryptography (needed by tools/mac-push-check.py)" >&2; exit 1; }

VERSION=$(cat "$HERE/VERSION" 2>/dev/null || date -u +%Y%m%d%H%M%S)
RELEASE="$ROOT/releases/$VERSION"

if ! id "$SERVICE_USER" >/dev/null 2>&1; then
	useradd --system --home-dir "$STATE" --no-create-home --shell /usr/sbin/nologin "$SERVICE_USER"
fi

install -d -m 0755 "$ROOT" "$ROOT/releases" "$NATIVE"
TMPDIR=$TMP
install -d -m 0700 -o "$SERVICE_USER" -g "$SERVICE_USER" "$STATE" "$LOGS" "$TMP"
export TMPDIR
"$HERE/setup-native.sh" "$NATIVE"

rm -rf "$RELEASE"
install -d -m 0755 "$RELEASE/bin" "$RELEASE/tools" "$RELEASE/nativeadi" "$RELEASE/gsa"
for binary in dashboard login trusted-device tunnelgate remote-gateway accountsetupcheck nacexchange familyinspect nativeadicheck reconnectvault; do
	[ -x "$HERE/bin/$binary" ] && install -m 0755 "$HERE/bin/$binary" "$RELEASE/bin/$binary"
done
for helper in mac-push-check.py mac-nac-check.py skill-store-transport.py; do
	install -m 0644 "$HERE/tools/$helper" "$RELEASE/tools/$helper"
done
install -m 0644 "$HERE/nativeadi/bridge.py" "$HERE/nativeadi/helper.py" "$RELEASE/nativeadi/"
install -m 0644 "$HERE/gsa/apple_root_ca.go" "$HERE/gsa/apple_root_ca_g3.pem" "$RELEASE/gsa/"
cp -a "$HERE/.cache" "$RELEASE/.cache"
find "$RELEASE/.cache" -type d -exec chmod 0755 {} +
find "$RELEASE/.cache" -type f -exec chmod 0644 {} +
for file in README-linux.md run-ui.sh setup-native.sh enable-public-gateway.sh reset-dashboard-password.sh appleservices.service appleservices.env VERSION SHA256SUMS LICENSE THIRD_PARTY_NOTICES.md DISCLAIMER.md; do
	[ -f "$HERE/$file" ] && install -m 0644 "$HERE/$file" "$RELEASE/$file"
done
chmod 0755 "$RELEASE/run-ui.sh" "$RELEASE/setup-native.sh" "$RELEASE/enable-public-gateway.sh" "$RELEASE/reset-dashboard-password.sh" 2>/dev/null || true
ln -sfn "$NATIVE" "$RELEASE/native"
ln -sfn "$RELEASE" "$ROOT/current.new" && mv -Tf "$ROOT/current.new" "$ROOT/current"
if [ ! -e /opt/appleservices ] && [ ! -L /opt/appleservices ]; then
	ln -s "$ROOT" /opt/appleservices
fi

install -d -m 0750 -o root -g "$SERVICE_USER" "$CONFIG"
if [ ! -f "$CONFIG/appleservices.env" ]; then
	install -m 0640 -o root -g "$SERVICE_USER" "$HERE/appleservices.env" "$CONFIG/appleservices.env"
fi
if [ ! -f "$CONFIG/credential.key" ]; then
	KEY_TMP=$(mktemp "$CONFIG/.credential-key.XXXXXX")
	chmod 0600 "$KEY_TMP"
	dd if=/dev/urandom of="$KEY_TMP" bs=32 count=1 status=none
	chown root:"$SERVICE_USER" "$KEY_TMP"
	chmod 0640 "$KEY_TMP"
	mv -f "$KEY_TMP" "$CONFIG/credential.key"
fi
if [ "$(wc -c < "$CONFIG/credential.key")" -ne 32 ]; then
	echo "invalid $CONFIG/credential.key (expected exactly 32 bytes)" >&2
	exit 1
fi
chown root:"$SERVICE_USER" "$CONFIG/credential.key"
chmod 0640 "$CONFIG/credential.key"

sed "s#/data/appleservices#$ROOT#g" "$HERE/appleservices.service" > "$UNIT"
chmod 0644 "$UNIT"
systemctl daemon-reload
systemctl enable appleservices.service >/dev/null
if [ "${1:-}" != "--no-start" ]; then
	systemctl restart appleservices.service
	sleep 1
	systemctl --no-pager --lines=5 status appleservices.service || true
fi

PORT=$(sed -n 's/^APPLESERVICES_PORT=//p' "$CONFIG/appleservices.env" | tail -n 1)
echo
echo "Installed $VERSION to $RELEASE ($ROOT/current -> $RELEASE)."
echo "Open the dashboard from your own computer through SSH:"
echo "  ssh -N -L ${PORT:-18741}:127.0.0.1:${PORT:-18741} <user>@<this-server>"
echo "  then browse to http://127.0.0.1:${PORT:-18741}/"
echo "Previous releases stay in $ROOT/releases for rollback."
