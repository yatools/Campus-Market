#!/usr/bin/env sh
set -eu

cd "$(dirname "$0")/.."
docker compose --profile tools run --rm certbot renew --webroot -w /var/www/certbot --quiet
docker compose exec -T web nginx -s reload

