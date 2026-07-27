#!/usr/bin/env sh
set -eu

# Let's Encrypt certificates last 90 days. Nothing in this repository schedules this
# script, so install it in the host crontab (bootstrap-tls.sh prints the exact line):
#
#   0 3 * * * cd /srv/wutong && sh deploy/renew-tls.sh >> /var/log/wutong-renew.log 2>&1
#
# Without it the site becomes unreachable on day 90, and because nginx sends
# Strict-Transport-Security with a one-year max-age, visitors cannot click through the
# certificate warning either.

cd "$(dirname "$0")/.."
DOMAIN=$(grep '^DOMAIN=' .env | tail -n 1 | cut -d= -f2-)
test -n "${DOMAIN:-}" || { echo "DOMAIN 未配置" >&2; exit 1; }

docker compose --profile tools run --rm certbot renew --webroot -w /var/www/certbot --quiet
docker compose exec -T web nginx -s reload

# Fail loudly when the certificate is still close to expiry, so a renewal that silently did
# nothing shows up in cron mail instead of surfacing as an outage two months later.
expires_at=$(docker compose --profile tools run --rm --entrypoint sh certbot -c \
  "openssl x509 -enddate -noout -in /etc/letsencrypt/live/$DOMAIN/fullchain.pem | cut -d= -f2" 2>/dev/null | tr -d '\r') || expires_at=""
if [ -n "$expires_at" ]; then
  expiry_epoch=$(date -u -d "$expires_at" +%s 2>/dev/null) || expiry_epoch=""
  if [ -n "$expiry_epoch" ]; then
    days=$(( (expiry_epoch - $(date -u +%s)) / 86400 ))
    echo "证书剩余有效期：$days 天"
    test "$days" -gt 10 || { echo "证书剩余有效期不足 10 天，续期可能未生效" >&2; exit 1; }
  fi
fi
