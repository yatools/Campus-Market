#!/usr/bin/env sh
set -eu

cd "$(dirname "$0")/.."
DOMAIN=$(grep '^DOMAIN=' .env | tail -n 1 | cut -d= -f2-)
test -n "${DOMAIN:-}" || { echo "DOMAIN 未配置" >&2; exit 1; }
test -n "${TLS_EMAIL:-}" || { echo "请在环境中设置 TLS_EMAIL" >&2; exit 1; }
docker compose stop web 2>/dev/null || true
docker compose --profile tools run --rm --service-ports certbot certonly \
  --standalone --non-interactive --agree-tos --email "$TLS_EMAIL" -d "$DOMAIN"
docker compose up -d web

