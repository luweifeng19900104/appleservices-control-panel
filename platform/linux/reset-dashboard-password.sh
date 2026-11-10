#!/bin/sh
set -eu

HERE=$(dirname -- "$0")
HERE=$(cd "$HERE" && pwd)
RELEASE=$(readlink -f "$HERE")
ROOT=$(dirname "$(dirname "$RELEASE")")
STATE=${APPLESERVICES_STATE_DIR:-$ROOT/state}
AUTH=$STATE/dashboard-auth.json

if [ "$(id -u)" -ne 0 ]; then
	echo "run with sudo: sudo $0" >&2
	exit 1
fi
case "$AUTH" in
	"$ROOT"/*/dashboard-auth.json) ;;
	*) echo "refusing unexpected auth path: $AUTH" >&2; exit 1 ;;
esac

systemctl stop appleservices.service
rm -f -- "$AUTH"
systemctl start appleservices.service
echo "Dashboard management password removed. Open the panel to create a new password."
