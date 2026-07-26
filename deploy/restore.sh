#!/usr/bin/env sh
set -eu

if [ "$#" -ne 1 ]; then echo "用法：deploy/restore.sh /path/to/wutong-backup.zip" >&2; exit 1; fi
ARCHIVE=$(realpath "$1")
cd "$(dirname "$0")/.."
CONFIG_FILE=${ENV_FILE:-.env}
test -f "$CONFIG_FILE" || { echo "配置文件不存在：$CONFIG_FILE" >&2; exit 1; }
compose() { docker compose --env-file "$CONFIG_FILE" "$@"; }
restart_app() {
  compose up -d api worker
  compose restart web
}
wait_ready() {
  origin=$1
  attempts=30
  while [ "$attempts" -gt 0 ]; do
    # Bound each probe: without --max-time the "30 seconds" in the failure message can be
    # tens of minutes if the endpoint hangs.
    if curl -fsS --connect-timeout 3 --max-time 5 "$origin/health/ready" >/dev/null 2>&1; then return 0; fi
    attempts=$((attempts - 1))
    sleep 1
  done
  echo "健康检查在 30 秒内未就绪：$origin/health/ready" >&2
  return 1
}
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
# cleanup runs on EXIT only. A POSIX handler that does not exit returns to the point of
# interruption, so binding it to INT/TERM meant Ctrl-C deleted the working directory and
# dropped the restore database, then carried on running the rest of the script.
trap cleanup EXIT
trap 'echo "已中止恢复" >&2; exit 130' INT TERM

unzip -Z1 "$ARCHIVE" | grep -Eq '(^|/)\.\.(/|$)|^/' && { echo "备份包包含越界路径" >&2; exit 1; } || true
# A name-only check cannot see symlinks: an archive holding a symlink "x" -> /etc plus a
# regular entry "x/evil" contains no ".." and no leading "/", yet unzip follows the link
# and writes outside $TMP. Reject any non-regular entry, then verify after extraction.
unzip -Z "$ARCHIVE" | grep -Eq '^l' && { echo "备份包包含符号链接条目" >&2; exit 1; } || true
unzip -q -o "$ARCHIVE" -d "$TMP"
test -z "$(find "$TMP" -type l -print -quit)" || { echo "备份包解压出符号链接" >&2; exit 1; }
test -f "$TMP/database.dump" || { echo "备份包缺少 database.dump" >&2; exit 1; }
test -f "$TMP/SHA256SUMS" || { echo "备份包缺少完整性校验文件" >&2; exit 1; }
(cd "$TMP" && sha256sum -c SHA256SUMS)
compose up -d db
# Tolerate an unreachable or missing production database: that is precisely the disaster
# this script exists for, and `set -e` on this command substitution aborted the restore
# before it started.
EXPECTED_VERSION=$(compose exec -T db psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Atqc "SELECT COALESCE(max(version_id),0) FROM goose_db_version WHERE is_applied=true" 2>/dev/null || echo "")
compose exec -T db pg_restore --list < "$TMP/database.dump" >/dev/null
compose exec -T db createdb -U "$POSTGRES_USER" "$RESTORE_DB"
compose exec -T db pg_restore -U "$POSTGRES_USER" -d "$RESTORE_DB" --exit-on-error --no-owner --no-privileges < "$TMP/database.dump"

VERSION=$(compose exec -T db psql -U "$POSTGRES_USER" -d "$RESTORE_DB" -Atqc "SELECT COALESCE(max(version_id),0) FROM goose_db_version WHERE is_applied=true")
if [ -n "$EXPECTED_VERSION" ]; then
  test "$VERSION" = "$EXPECTED_VERSION" || { echo "恢复库迁移版本不匹配：$VERSION（期望 $EXPECTED_VERSION）" >&2; exit 1; }
else
  # 生产库不可达（库被删、卷损坏——正是需要恢复的场景）时无从取得期望版本。
  # 此时不能拿空串去比较，否则必然「不匹配」而中止；改为放行并显式告警：
  # 恢复完成后如版本落后于当前代码，需要再跑一次 migrate up。
  echo "警告：无法读取现网迁移版本，跳过版本一致性校验。归档版本为 $VERSION，" >&2
  echo "      恢复后请执行 compose run --rm migrate 确认 schema 已是最新。" >&2
fi
compose exec -T db psql -U "$POSTGRES_USER" -d "$RESTORE_DB" -v ON_ERROR_STOP=1 -Atqc "SET CONSTRAINTS ALL IMMEDIATE; SELECT 1" >/dev/null
checked_tables=0
if [ -f "$TMP/TABLE_COUNTS.tsv" ]; then
  while IFS="$(printf '\t')" read -r table expected; do
    case "$table" in users|content_entities|attachments|listings|market_transactions|market_disputes|market_reviews|messages|audit_logs) ;; *) echo "非法表计数项：$table" >&2; exit 1;; esac
    # </dev/null is essential: `docker compose exec -T` forwards this shell's stdin, which
    # here is TABLE_COUNTS.tsv itself. Without it the first iteration consumed every
    # remaining line and only one table was ever verified.
    actual=$(compose exec -T db psql -U "$POSTGRES_USER" -d "$RESTORE_DB" -Atqc "SELECT count(*) FROM $table" </dev/null)
    test "$actual" = "$expected" || { echo "表 $table 行数不一致：$actual != $expected" >&2; exit 1; }
    checked_tables=$((checked_tables + 1))
  done < "$TMP/TABLE_COUNTS.tsv"
  echo "已校验 $checked_tables 张表的行数"
fi
if [ -f "$TMP/OBJECTS.tsv" ]; then compose run -T --rm api verify-storage-manifest < "$TMP/OBJECTS.tsv"; fi

compose stop api worker
compose exec -T db psql -U "$POSTGRES_USER" -d postgres -v ON_ERROR_STOP=1 -c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname IN ('$POSTGRES_DB','$RESTORE_DB') AND pid<>pg_backend_pid();"
# Both renames in a single psql invocation, so the window in which no database is named
# $POSTGRES_DB is one round trip rather than two. If it still fails, print the exact
# recovery command — the _rollback_<STAMP> naming is otherwise undocumented.
if ! compose exec -T db psql -U "$POSTGRES_USER" -d postgres -v ON_ERROR_STOP=1 \
  -c "ALTER DATABASE \"$POSTGRES_DB\" RENAME TO \"$ROLLBACK_DB\"; ALTER DATABASE \"$RESTORE_DB\" RENAME TO \"$POSTGRES_DB\";"; then
  if ! compose exec -T db psql -U "$POSTGRES_USER" -d postgres -v ON_ERROR_STOP=1 -c "ALTER DATABASE \"$ROLLBACK_DB\" RENAME TO \"$POSTGRES_DB\";"; then
    echo "严重：数据库切换失败且自动回滚也失败。请手工执行：" >&2
    echo "  ALTER DATABASE \"$ROLLBACK_DB\" RENAME TO \"$POSTGRES_DB\";" >&2
  fi
  restart_app
  exit 1
fi
SWAPPED=1
restart_app
ORIGIN=$(grep '^PUBLIC_ORIGIN=' "$CONFIG_FILE" | tail -n 1 | cut -d= -f2-)
if ! wait_ready "$ORIGIN"; then
  echo "新数据库健康检查失败，正在自动回滚" >&2
  compose stop api worker
  compose exec -T db psql -U "$POSTGRES_USER" -d postgres -v ON_ERROR_STOP=1 -c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname IN ('$POSTGRES_DB','$ROLLBACK_DB') AND pid<>pg_backend_pid();"
  compose exec -T db psql -U "$POSTGRES_USER" -d postgres -v ON_ERROR_STOP=1 -c "ALTER DATABASE \"$POSTGRES_DB\" RENAME TO \"$FAILED_DB\";"
  compose exec -T db psql -U "$POSTGRES_USER" -d postgres -v ON_ERROR_STOP=1 -c "ALTER DATABASE \"$ROLLBACK_DB\" RENAME TO \"$POSTGRES_DB\";"
  restart_app
  exit 1
fi
# The previous production database is kept by default. Readiness only checks connectivity,
# the migration version and object storage — none of which can tell a correct backup from a
# stale one, so dropping it 30 seconds after the swap made an accidental restore from an
# old archive unrecoverable.
if [ "${RESTORE_DROP_OLD:-0}" = "1" ]; then
  compose exec -T db dropdb -U "$POSTGRES_USER" --if-exists "$ROLLBACK_DB"
  echo "恢复完成并通过健康检查（原库 $ROLLBACK_DB 已删除）"
else
  echo "恢复完成并通过健康检查"
  echo "原数据库已保留为 $ROLLBACK_DB。确认业务数据无误后再执行："
  echo "  compose exec -T db dropdb -U \"$POSTGRES_USER\" --if-exists \"$ROLLBACK_DB\""
  echo "（或在下次恢复时设置 RESTORE_DROP_OLD=1 自动删除）"
fi
