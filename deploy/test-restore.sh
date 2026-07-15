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

cleanup() {
  compose down --volumes --remove-orphans >/dev/null 2>&1 || true
  rm -rf "$TMP"
}
trap cleanup EXIT INT TERM

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

for _ in $(seq 1 60); do
  if curl -fsS http://127.0.0.1:8080/health/ready >/dev/null; then
    break
  fi
  sleep 1
done
curl -fsS http://127.0.0.1:8080/health/ready >/dev/null

compose exec -T db psql -U wutong -d wutong -v ON_ERROR_STOP=1 -c \
  "UPDATE market_categories SET name='archive-state' WHERE id=(SELECT min(id) FROM market_categories);" >/dev/null

ARCHIVE_DIR="$TMP/archive"
mkdir -p "$ARCHIVE_DIR"
compose exec -T db pg_dump -U wutong -d wutong --format=custom >"$ARCHIVE_DIR/database.dump"
USER_COUNT=$(compose exec -T db psql -U wutong -d wutong -Atqc "SELECT count(*) FROM users")
printf 'users\t%s\n' "$USER_COUNT" >"$ARCHIVE_DIR/TABLE_COUNTS.tsv"
(
  cd "$ARCHIVE_DIR"
  sha256sum database.dump TABLE_COUNTS.tsv >SHA256SUMS
  zip -q "$TMP/restore.zip" database.dump TABLE_COUNTS.tsv SHA256SUMS
)

ENV_FILE="$ENV_FILE" sh deploy/restore.sh "$TMP/restore.zip"
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
curl -fsS http://127.0.0.1:8080/health/ready >/dev/null

echo "restore success and readiness-failure rollback verified"
