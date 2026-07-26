#!/usr/bin/env sh
set -eu

cd "$(dirname "$0")/.."
test -f .env || { echo "missing .env" >&2; exit 1; }
# 拒绝原样上生产的占位符与弱口令。.env*.example 里的示例值必须被替换掉；
# 之前 S3 凭据给的是能直接工作的 minioadmin，看起来不像占位符，最容易被漏改。
if grep -Eq '^[A-Z_]+=.*(replace-with|minioadmin|change-me|development-only)' .env; then
  echo ".env 中仍存在示例/占位凭据，请先替换后再部署" >&2
  grep -nE '^[A-Z_]+=.*(replace-with|minioadmin|change-me|development-only)' .env >&2
  exit 1
fi
test "$#" -eq 3 || { echo "usage: $0 <api-image@sha256:digest> <worker-image@sha256:digest> <web-image@sha256:digest>" >&2; exit 1; }

is_digest_reference() {
  reference=$1
  case "$reference" in
    *@sha256:*) ;;
    *) return 1 ;;
  esac
  digest=${reference##*@sha256:}
  case "$digest" in
    ""|*[!0123456789abcdef]*) return 1 ;;
  esac
  test "${#digest}" -eq 64
}

if ! is_digest_reference "$1" || ! is_digest_reference "$2" || ! is_digest_reference "$3"; then
  echo "all application images must be immutable sha256 digest references" >&2
  exit 1
fi

state_dir=.deploy-state
current="$state_dir/current.env"
previous="$state_dir/previous.env"
candidate="$state_dir/candidate.env"
mkdir -p "$state_dir"
# Serialise deployments. Two concurrent runs shared one candidate.env, so the second
# overwrote the first mid-flight and the first then deployed — and recorded — the other
# run's images, leaving current/previous.env describing a state that never existed.
if ! mkdir "$state_dir/.lock" 2>/dev/null; then
  echo "另一个部署正在进行（如确认无部署在跑，请删除 $state_dir/.lock）" >&2
  exit 1
fi
test ! -f "$current" || cp "$current" "$previous"
printf 'API_IMAGE=%s\nWORKER_IMAGE=%s\nWEB_IMAGE=%s\n' "$1" "$2" "$3" > "$candidate"

compose() {
  docker compose --env-file .env --env-file "$candidate" "$@"
}

rollback() {
  test -f "$previous" || return 0
  cp "$previous" "$candidate"
  compose pull api worker web || true
  compose up -d api worker web || true
  # Only the images are rolled back; the migration that already ran is not. The API's
  # readiness check accepts a database ahead of the binary (version >= expected), so the
  # previous release still comes up — but a migration that is not backward compatible
  # needs a manual `migrate down`, and the operator has to be told that.
  echo "已回滚到上一版本镜像。注意：本次部署已执行的数据库迁移未回滚。" >&2
  echo "若新迁移与旧版本不兼容，请手工执行：compose run --rm migrate-down" >&2
}
deployed=false
finish() {
  status=$?
  trap - 0
  if [ "$deployed" != true ]; then
    rollback
  fi
  rmdir "$state_dir/.lock" 2>/dev/null || true
  exit "$status"
}
trap finish 0
trap 'exit 1' INT TERM HUP

compose pull api worker web
compose up -d db
compose run --rm migrate
compose run --rm api verify-config
compose up -d api worker web
domain=$(grep '^DOMAIN=' .env | tail -n 1 | cut -d= -f2-)
test -n "$domain" || { echo "\.env 中缺少 DOMAIN" >&2; exit 1; }
# --retry alone does not retry connection-refused, and web has just been recreated: the
# first probe routinely lands in the second before nginx is listening, which failed the
# deploy and triggered a rollback of a perfectly good release.
if ! curl -fsS --retry 12 --retry-delay 5 --retry-connrefused --retry-all-errors \
  --connect-timeout 5 --max-time 15 "https://$domain/health/ready"; then
  exit 1
fi
compose ps
mv "$candidate" "$current"
deployed=true
