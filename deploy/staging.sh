#!/usr/bin/env sh
set -eu

cd "$(dirname "$0")/.."
test -f .env.staging || { echo "缺少 .env.staging，请从 .env.staging.example 创建" >&2; exit 1; }
COMPOSE="docker compose -p wutong-staging --env-file .env.staging -f docker-compose.yml -f docker-compose.staging.yml"
$COMPOSE build
$COMPOSE up -d db
$COMPOSE run --rm migrate
$COMPOSE run --rm api verify-config
$COMPOSE up -d api worker web
$COMPOSE ps
curl -fsS http://127.0.0.1:8080/health/ready
