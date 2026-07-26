#!/usr/bin/env sh
set -eu

cd "$(dirname "$0")/.."
DOMAIN=$(grep '^DOMAIN=' .env | tail -n 1 | cut -d= -f2-)
test -n "${DOMAIN:-}" || { echo "DOMAIN 未配置" >&2; exit 1; }
test -n "${TLS_EMAIL:-}" || { echo "请在环境中设置 TLS_EMAIL" >&2; exit 1; }

# Bring web back up no matter how this script ends. `docker compose stop` clears the
# restart policy, so a certbot failure (DNS not yet propagated, Let's Encrypt rate limit,
# port 80 already bound) used to leave the site with nothing listening on 80/443 — and
# this script is most often re-run precisely when certificates are already misbehaving.
trap 'docker compose up -d web >/dev/null 2>&1 || true' EXIT
trap 'echo "已中止" >&2; exit 130' INT TERM

docker compose stop web 2>/dev/null || true
docker compose --profile tools run --rm --service-ports certbot certonly \
  --standalone --non-interactive --agree-tos --email "$TLS_EMAIL" -d "$DOMAIN"

# Switch the saved renewal configuration to the webroot authenticator so renew-tls.sh —
# which runs while web holds port 80 — can actually work. certbot replays whatever
# authenticator it recorded at issuance, so a standalone-issued certificate would keep
# trying (and failing) to bind port 80 at every renewal.
docker compose up -d web
sleep 3
docker compose --profile tools run --rm certbot certonly \
  --webroot -w /var/www/certbot --non-interactive --agree-tos --email "$TLS_EMAIL" \
  -d "$DOMAIN" --keep-until-expiring --cert-name "$DOMAIN"

echo "证书已签发。请把自动续期加入宿主机 crontab（证书有效期仅 90 天）："
echo "  0 3 * * * cd $(pwd) && sh deploy/renew-tls.sh >> /var/log/wutong-renew.log 2>&1"
