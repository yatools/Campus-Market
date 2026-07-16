#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

mkdir -p "$work/deploy" "$work/bin" "$work/.deploy-state"
cp "$root/deploy/deploy.sh" "$work/deploy/deploy.sh"
touch "$work/.env"

printf '%s\n' '#!/usr/bin/env sh' 'printf "%s\\n" "$*" >> "${DEPLOY_LOG:?}"' 'exit 0' > "$work/bin/docker"
printf '%s\n' '#!/usr/bin/env sh' 'test "${FAIL_READINESS:-}" != 1' > "$work/bin/curl"
chmod +x "$work/bin/docker" "$work/bin/curl"

old_api="registry.test/api@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
old_worker="registry.test/worker@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
old_web="registry.test/web@sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
new_api="registry.test/api@sha256:1111111111111111111111111111111111111111111111111111111111111111"
new_worker="registry.test/worker@sha256:2222222222222222222222222222222222222222222222222222222222222222"
new_web="registry.test/web@sha256:3333333333333333333333333333333333333333333333333333333333333333"

printf 'API_IMAGE=%s\nWORKER_IMAGE=%s\nWEB_IMAGE=%s\n' "$old_api" "$old_worker" "$old_web" > "$work/.deploy-state/current.env"
printf 'DOMAIN=wall.test\n' > "$work/.env"

DEPLOY_LOG="$work/success.log" PATH="$work/bin:$PATH" sh "$work/deploy/deploy.sh" "$new_api" "$new_worker" "$new_web"
grep -Fqx "API_IMAGE=$new_api" "$work/.deploy-state/current.env"
grep -Fqx "WORKER_IMAGE=$new_worker" "$work/.deploy-state/current.env"
grep -Fqx "WEB_IMAGE=$new_web" "$work/.deploy-state/current.env"

if FAIL_READINESS=1 DEPLOY_LOG="$work/failure.log" PATH="$work/bin:$PATH" sh "$work/deploy/deploy.sh" "$old_api" "$old_worker" "$old_web"; then
  echo "deployment unexpectedly succeeded after readiness failure" >&2
  exit 1
fi
grep -Fqx "API_IMAGE=$new_api" "$work/.deploy-state/candidate.env"
grep -Fqx "WORKER_IMAGE=$new_worker" "$work/.deploy-state/candidate.env"
grep -Fqx "WEB_IMAGE=$new_web" "$work/.deploy-state/candidate.env"
grep -F 'up -d api worker web' "$work/failure.log" >/dev/null

if DEPLOY_LOG="$work/invalid.log" PATH="$work/bin:$PATH" sh "$work/deploy/deploy.sh" not-a-digest "$new_worker" "$new_web"; then
  echo "mutable image reference was accepted" >&2
  exit 1
fi
