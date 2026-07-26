#!/usr/bin/env bash
set -euo pipefail

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
TMP=$(mktemp -d)
export ENV_FILE="$TMP/restore.env"
export COMPOSE_PROJECT_NAME="wutong-restore-ci"
export COMPOSE_FILE="$ROOT/docker-compose.yml:$ROOT/docker-compose.dev.yml"

compose() {
  docker compose --env-file "$ENV_FILE" "$@"
}

wait_ready() {
  origin=$1
  for _ in $(seq 1 60); do
    if curl -fsS "$origin/health/ready" >/dev/null 2>&1; then return 0; fi
    sleep 1
  done
  echo "health endpoint did not become ready: $origin/health/ready" >&2
  return 1
}

cleanup() {
  status=$?
  trap - EXIT INT TERM
  if [ "$status" -ne 0 ]; then
    echo "restore drill failed; container status follows" >&2
    compose ps -a >&2 || true
    echo "restore drill service logs follow" >&2
    compose logs --no-color minio minio-init migrate api web >&2 || true
  fi
  compose down --volumes --remove-orphans >/dev/null 2>&1 || true
  rm -rf "$TMP"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

cat >"$ENV_FILE" <<'EOF'
ENVIRONMENT=development
DOMAIN=localhost
PUBLIC_ORIGIN=http://127.0.0.1:8080
API_PREFIX=/api/v1
TRUSTED_HOSTS=127.0.0.1,localhost
TRUSTED_PROXY_CIDRS=172.16.0.0/12
HEALTH_CHECK_TOKEN=restore-ci-health-token
SECRET_KEY=restore-ci-secret-key-that-is-long-enough
POSTGRES_DB=wutong
POSTGRES_USER=wutong
POSTGRES_PASSWORD=restore-ci-password
DATABASE_URL=postgresql://wutong:restore-ci-password@db:5432/wutong
DB_POOL_SIZE=5
DB_MAX_OVERFLOW=2
AUTO_CREATE_SCHEMA=false
ALLOWED_CAMPUS_EMAIL_DOMAINS=test.edu.cn
SESSION_COOKIE_NAME=wutong_session
CSRF_COOKIE_NAME=wutong_csrf
COOKIE_SECURE=false
SESSION_ROTATION_HOURS=24
SESSION_SLIDING_DAYS=7
SESSION_ABSOLUTE_DAYS=30
MAX_UPLOAD_MB=8
S3_ENDPOINT=http://minio:9000
S3_REGION=us-east-1
S3_ACCESS_KEY_ID=minioadmin
S3_SECRET_ACCESS_KEY=minioadmin
S3_PUBLIC_BUCKET=wutong-public
S3_PRIVATE_BUCKET=wutong-private
S3_BACKUP_BUCKET=wutong-backups
S3_PUBLIC_BASE_URL=http://127.0.0.1:9000/wutong-public
S3_USE_PATH_STYLE=true
MARKET_RESERVATION_HOURS=24
MARKET_REVIEW_BLIND_DAYS=14
SMTP_HOST=
SMTP_PORT=465
SMTP_USERNAME=
SMTP_PASSWORD=
SMTP_FROM=
SMTP_USE_SSL=true
DOCS_ENABLED=true
LOG_LEVEL=INFO
WORKER_POLL_SECONDS=1
EOF

cd "$ROOT"
compose up -d --build
wait_ready http://127.0.0.1:8080

compose exec -T db psql -U wutong -d wutong -v ON_ERROR_STOP=1 -c \
  "UPDATE market_categories SET name='archive-state' WHERE id=(SELECT min(id) FROM market_categories);" >/dev/null

ARCHIVE_DIR="$TMP/archive"
mkdir -p "$ARCHIVE_DIR"
compose exec -T db pg_dump -U wutong -d wutong --format=custom >"$ARCHIVE_DIR/database.dump"
# 至少写两行：restore.sh 的表计数循环曾被 `docker compose exec -T` 吃掉 stdin，
# 只校验了第一张表就静默结束。单行清单恰好掩盖了这个缺陷，因此这里必须多于一行。
USER_COUNT=$(compose exec -T db psql -U wutong -d wutong -Atqc "SELECT count(*) FROM users")
ENTITY_COUNT=$(compose exec -T db psql -U wutong -d wutong -Atqc "SELECT count(*) FROM content_entities")
ATTACHMENT_COUNT=$(compose exec -T db psql -U wutong -d wutong -Atqc "SELECT count(*) FROM attachments")
{
  printf 'users\t%s\n' "$USER_COUNT"
  printf 'content_entities\t%s\n' "$ENTITY_COUNT"
  printf 'attachments\t%s\n' "$ATTACHMENT_COUNT"
} >"$ARCHIVE_DIR/TABLE_COUNTS.tsv"
(
  cd "$ARCHIVE_DIR"
  sha256sum database.dump TABLE_COUNTS.tsv >SHA256SUMS
  zip -q "$TMP/restore.zip" database.dump TABLE_COUNTS.tsv SHA256SUMS
)

RESTORE_OUTPUT=$(ENV_FILE="$ENV_FILE" sh deploy/restore.sh "$TMP/restore.zip" 2>&1 | tee /dev/stderr)
# 断言三张表都被校验过，而不是只校验了第一张。
echo "$RESTORE_OUTPUT" | grep -Fq "已校验 3 张表的行数" || {
  echo "恢复流程没有校验全部表计数（表计数循环可能又被 stdin 吞掉了）" >&2
  exit 1
}
# 原生产库默认保留，便于误恢复后回退。
echo "$RESTORE_OUTPUT" | grep -Fq "原数据库已保留为" || {
  echo "恢复完成后未保留原数据库" >&2
  exit 1
}
RESTORED=$(compose exec -T db psql -U wutong -d wutong -Atqc "SELECT name FROM market_categories ORDER BY id LIMIT 1")
test "$RESTORED" = "archive-state"

compose exec -T db psql -U wutong -d wutong -v ON_ERROR_STOP=1 -c \
  "UPDATE market_categories SET name='live-state' WHERE id=(SELECT min(id) FROM market_categories);" >/dev/null
sed -i 's#^PUBLIC_ORIGIN=.*#PUBLIC_ORIGIN=http://127.0.0.1:9#' "$ENV_FILE"

if ENV_FILE="$ENV_FILE" sh deploy/restore.sh "$TMP/restore.zip"; then
  echo "restore unexpectedly succeeded with a failing readiness endpoint" >&2
  exit 1
fi

ROLLED_BACK=$(compose exec -T db psql -U wutong -d wutong -Atqc "SELECT name FROM market_categories ORDER BY id LIMIT 1")
test "$ROLLED_BACK" = "live-state"
sed -i 's#^PUBLIC_ORIGIN=.*#PUBLIC_ORIGIN=http://127.0.0.1:8080#' "$ENV_FILE"
compose up -d api worker
compose restart web
wait_ready http://127.0.0.1:8080

echo "restore success and readiness-failure rollback verified"
