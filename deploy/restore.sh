#!/usr/bin/env sh
set -eu

if [ "$#" -ne 1 ]; then echo "用法：deploy/restore.sh /path/to/wutong-backup.zip" >&2; exit 1; fi
ARCHIVE=$(realpath "$1")
cd "$(dirname "$0")/.."
CONFIG_FILE=${ENV_FILE:-.env}
test -f "$CONFIG_FILE" || { echo "配置文件不存在：$CONFIG_FILE" >&2; exit 1; }
compose() { docker compose --env-file "$CONFIG_FILE" "$@"; }
POSTGRES_USER=$(grep '^POSTGRES_USER=' "$CONFIG_FILE" | tail -n 1 | cut -d= -f2-)
POSTGRES_DB=$(grep '^POSTGRES_DB=' "$CONFIG_FILE" | tail -n 1 | cut -d= -f2-)
test -n "$POSTGRES_USER" && test -n "$POSTGRES_DB" || { echo "数据库配置缺失" >&2; exit 1; }
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
unzip -q "$ARCHIVE" -d "$TMP"
test -f "$TMP/database.dump" || { echo "备份包缺少 database.dump" >&2; exit 1; }
test -f "$TMP/SHA256SUMS" || { echo "备份包缺少完整性校验文件" >&2; exit 1; }
(cd "$TMP" && sha256sum -c SHA256SUMS)
compose stop api worker
compose exec -T db dropdb -U "$POSTGRES_USER" --if-exists "$POSTGRES_DB"
compose exec -T db createdb -U "$POSTGRES_USER" "$POSTGRES_DB"
compose exec -T db pg_restore -U "$POSTGRES_USER" -d "$POSTGRES_DB" --clean --if-exists < "$TMP/database.dump"
if [ -d "$TMP/uploads" ]; then compose cp "$TMP/uploads/." api:/data/uploads/; fi
compose run --rm migrate
compose up -d api worker
compose exec -T db psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -v ON_ERROR_STOP=1 -c 'SELECT 1' >/dev/null
curl -fsS "$(grep '^PUBLIC_ORIGIN=' "$CONFIG_FILE" | tail -n 1 | cut -d= -f2-)/health/ready"
