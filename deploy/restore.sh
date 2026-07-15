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
case "$POSTGRES_USER:$POSTGRES_DB" in *[!A-Za-z0-9_:.-]*) echo "数据库名或用户包含不安全字符" >&2; exit 1;; esac
test -n "$POSTGRES_USER" && test -n "$POSTGRES_DB" || { echo "数据库配置缺失" >&2; exit 1; }

STAMP=$(date -u +%Y%m%d%H%M%S)
RESTORE_DB="${POSTGRES_DB}_restore_${STAMP}"
ROLLBACK_DB="${POSTGRES_DB}_rollback_${STAMP}"
FAILED_DB="${POSTGRES_DB}_failed_${STAMP}"
TMP=$(mktemp -d)
SWAPPED=0
cleanup() {
  if [ "$SWAPPED" -eq 0 ]; then compose exec -T db dropdb -U "$POSTGRES_USER" --if-exists "$RESTORE_DB" >/dev/null 2>&1 || true; fi
  rm -rf "$TMP"
}
trap cleanup EXIT INT TERM

unzip -Z1 "$ARCHIVE" | grep -Eq '(^|/)\.\.(/|$)|^/' && { echo "备份包包含越界路径" >&2; exit 1; } || true
unzip -q "$ARCHIVE" -d "$TMP"
test -f "$TMP/database.dump" || { echo "备份包缺少 database.dump" >&2; exit 1; }
test -f "$TMP/SHA256SUMS" || { echo "备份包缺少完整性校验文件" >&2; exit 1; }
(cd "$TMP" && sha256sum -c SHA256SUMS)
compose up -d db
compose exec -T db pg_restore --list < "$TMP/database.dump" >/dev/null
compose exec -T db createdb -U "$POSTGRES_USER" "$RESTORE_DB"
compose exec -T db pg_restore -U "$POSTGRES_USER" -d "$RESTORE_DB" --exit-on-error --no-owner --no-privileges < "$TMP/database.dump"

VERSION=$(compose exec -T db psql -U "$POSTGRES_USER" -d "$RESTORE_DB" -Atqc "SELECT COALESCE(max(version_id),0) FROM goose_db_version WHERE is_applied=true")
test "$VERSION" = "1" || { echo "恢复库迁移版本不匹配：$VERSION" >&2; exit 1; }
compose exec -T db psql -U "$POSTGRES_USER" -d "$RESTORE_DB" -v ON_ERROR_STOP=1 -Atqc "SET CONSTRAINTS ALL IMMEDIATE; SELECT 1" >/dev/null
if [ -f "$TMP/TABLE_COUNTS.tsv" ]; then
  while IFS="$(printf '\t')" read -r table expected; do
    case "$table" in users|content_entities|attachments|listings|market_transactions|market_disputes|market_reviews|messages|audit_logs) ;; *) echo "非法表计数项：$table" >&2; exit 1;; esac
    actual=$(compose exec -T db psql -U "$POSTGRES_USER" -d "$RESTORE_DB" -Atqc "SELECT count(*) FROM $table")
    test "$actual" = "$expected" || { echo "表 $table 行数不一致：$actual != $expected" >&2; exit 1; }
  done < "$TMP/TABLE_COUNTS.tsv"
fi
if [ -f "$TMP/OBJECTS.tsv" ]; then compose run -T --rm api verify-storage-manifest < "$TMP/OBJECTS.tsv"; fi

compose stop api worker
compose exec -T db psql -U "$POSTGRES_USER" -d postgres -v ON_ERROR_STOP=1 -c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname IN ('$POSTGRES_DB','$RESTORE_DB') AND pid<>pg_backend_pid();"
compose exec -T db psql -U "$POSTGRES_USER" -d postgres -v ON_ERROR_STOP=1 -c "ALTER DATABASE \"$POSTGRES_DB\" RENAME TO \"$ROLLBACK_DB\";"
if ! compose exec -T db psql -U "$POSTGRES_USER" -d postgres -v ON_ERROR_STOP=1 -c "ALTER DATABASE \"$RESTORE_DB\" RENAME TO \"$POSTGRES_DB\";"; then
  compose exec -T db psql -U "$POSTGRES_USER" -d postgres -v ON_ERROR_STOP=1 -c "ALTER DATABASE \"$ROLLBACK_DB\" RENAME TO \"$POSTGRES_DB\";"
  compose up -d api worker
  exit 1
fi
SWAPPED=1
compose up -d api worker
ORIGIN=$(grep '^PUBLIC_ORIGIN=' "$CONFIG_FILE" | tail -n 1 | cut -d= -f2-)
if ! curl -fsS "$ORIGIN/health/ready"; then
  echo "新数据库健康检查失败，正在自动回滚" >&2
  compose stop api worker
  compose exec -T db psql -U "$POSTGRES_USER" -d postgres -v ON_ERROR_STOP=1 -c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname IN ('$POSTGRES_DB','$ROLLBACK_DB') AND pid<>pg_backend_pid();"
  compose exec -T db psql -U "$POSTGRES_USER" -d postgres -v ON_ERROR_STOP=1 -c "ALTER DATABASE \"$POSTGRES_DB\" RENAME TO \"$FAILED_DB\";"
  compose exec -T db psql -U "$POSTGRES_USER" -d postgres -v ON_ERROR_STOP=1 -c "ALTER DATABASE \"$ROLLBACK_DB\" RENAME TO \"$POSTGRES_DB\";"
  compose up -d api worker
  exit 1
fi
compose exec -T db dropdb -U "$POSTGRES_USER" --if-exists "$ROLLBACK_DB"
echo "恢复完成并通过健康检查"
