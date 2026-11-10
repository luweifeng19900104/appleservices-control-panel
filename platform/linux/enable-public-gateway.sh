#!/bin/sh
# Publish the loopback dashboard through an HTTPS access gate without changing
# the dashboard listener. Requires an existing IP-valid TLS certificate.
#
#   sudo ./enable-public-gateway.sh <public-ip> [port]
#
# The random gateway key persists across restarts. Delete
# /var/lib/appleservices-gateway/gate.key and restart the gateway to rotate it.
set -eu

PUBLIC_IP=${1:-}
PORT=${2:-18742}
ROOT=${APPLESERVICES_ROOT:-/data/appleservices}
CERT=${APPLESERVICES_GATEWAY_CERT:-/etc/letsencrypt/live/$PUBLIC_IP/fullchain.pem}
CERT_KEY=${APPLESERVICES_GATEWAY_CERT_KEY:-/etc/letsencrypt/live/$PUBLIC_IP/privkey.pem}
UNIT=/etc/systemd/system/appleservices-gateway.service
NGINX_CONF=/etc/nginx/conf.d/appleservices-gateway.conf

if [ "$(id -u)" -ne 0 ]; then
	echo "run with sudo: sudo $0 <public-ip> [port]" >&2
	exit 1
fi
if [ -z "$PUBLIC_IP" ]; then
	echo "public IPv4 address is required" >&2
	exit 2
fi
python3 -c 'import ipaddress,sys; value=ipaddress.ip_address(sys.argv[1]); assert value.version == 4 and value.is_global' "$PUBLIC_IP" 2>/dev/null || {
	echo "invalid public IPv4 address" >&2
	exit 2
}
case "$PORT" in
	''|*[!0-9]*) echo "invalid port" >&2; exit 2 ;;
esac
if [ "$PORT" -lt 1024 ] || [ "$PORT" -gt 65535 ]; then
	echo "port must be between 1024 and 65535" >&2
	exit 2
fi

for command in nginx openssl systemctl python3; do
	command -v "$command" >/dev/null 2>&1 || { echo "missing $command" >&2; exit 1; }
done
[ -x "$ROOT/current/bin/tunnelgate" ] || { echo "missing $ROOT/current/bin/tunnelgate" >&2; exit 1; }
if [ ! -r "$CERT" ] || [ ! -r "$CERT_KEY" ]; then
	echo "missing TLS certificate for $PUBLIC_IP" >&2
	exit 1
fi
openssl x509 -in "$CERT" -noout -checkend 86400 >/dev/null || { echo "TLS certificate is expired or expires within 24 hours" >&2; exit 1; }
openssl x509 -in "$CERT" -noout -ext subjectAltName | grep -F "IP Address:$PUBLIC_IP" >/dev/null || {
	echo "TLS certificate does not cover $PUBLIC_IP" >&2
	exit 1
}

cat > "$UNIT" <<EOF
[Unit]
Description=appleservices HTTPS public access gate
After=network-online.target appleservices.service
Wants=network-online.target
Requires=appleservices.service

[Service]
Type=simple
User=www-data
Group=www-data
RuntimeDirectory=appleservices-gateway
RuntimeDirectoryMode=0700
StateDirectory=appleservices-gateway
StateDirectoryMode=0700
ExecStart=$ROOT/current/bin/tunnelgate -socket /run/appleservices-gateway/gate.sock -upstream 127.0.0.1:18741 -key-file /var/lib/appleservices-gateway/gate.key
Restart=on-failure
RestartSec=5
UMask=0077
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
ProtectClock=yes
RestrictSUIDSGID=yes
RestrictNamespaces=yes
LockPersonality=yes
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
CapabilityBoundingSet=
AmbientCapabilities=

[Install]
WantedBy=multi-user.target
EOF
chmod 0644 "$UNIT"

TEMP_CONF=$(mktemp)
trap 'rm -f "$TEMP_CONF"' EXIT
cat > "$TEMP_CONF" <<EOF
upstream appleservices_public_gateway {
    server unix:/run/appleservices-gateway/gate.sock;
    keepalive 8;
}

server {
    listen 0.0.0.0:$PORT ssl;
    listen [::]:$PORT ssl;
    server_name $PUBLIC_IP;

    ssl_certificate $CERT;
    ssl_certificate_key $CERT_KEY;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_session_cache shared:AppleservicesGateway:10m;
    ssl_session_timeout 1d;
    ssl_session_tickets off;

    server_tokens off;
    client_max_body_size 2m;
    access_log /var/log/nginx/appleservices-gateway.access.log;
    error_log /var/log/nginx/appleservices-gateway.error.log warn;

    location / {
        proxy_pass http://appleservices_public_gateway;
        proxy_http_version 1.1;
        proxy_set_header Host \$http_host;
        proxy_set_header Connection "";
        proxy_set_header X-Forwarded-Proto https;
        proxy_set_header X-Forwarded-For "";
        proxy_set_header X-Real-IP "";
        proxy_read_timeout 120s;
        proxy_send_timeout 120s;
        proxy_buffering off;
    }
}
EOF
install -m 0644 "$TEMP_CONF" "$NGINX_CONF"

systemctl daemon-reload
systemctl enable --now appleservices-gateway.service >/dev/null
if ! nginx -t; then
	rm -f "$NGINX_CONF"
	nginx -t >/dev/null 2>&1 || true
	echo "invalid Nginx gateway configuration; rolled back" >&2
	exit 1
fi
systemctl reload nginx
if command -v ufw >/dev/null 2>&1 && ufw status | grep -q '^Status: active'; then
	ufw allow "$PORT/tcp" >/dev/null
fi

ACCESS_KEY=$(cat /var/lib/appleservices-gateway/gate.key)
echo "Public gateway enabled:"
echo "https://$PUBLIC_IP:$PORT/?k=$ACCESS_KEY"
echo "Set the dashboard management password immediately after opening the URL."
