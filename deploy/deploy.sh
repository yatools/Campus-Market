#!/usr/bin/env sh
set -eu

cd "$(dirname "$0")/.."
test -f .env || { echo "缺少 .env，请从 .env.example 创建" >&2; exit 1; }
docker compose build
docker compose up -d db
docker compose run --rm migrate
docker compose run --rm api verify-config
docker compose up -d api worker web
docker compose ps
curl -fsS https://"$(grep '^DOMAIN=' .env | cut -d= -f2)"/health/ready
