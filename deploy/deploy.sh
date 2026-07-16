#!/usr/bin/env sh
set -eu

cd "$(dirname "$0")/.."
test -f .env || { echo "missing .env" >&2; exit 1; }
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
}
deployed=false
finish() {
  status=$?
  trap - 0
  if [ "$deployed" != true ]; then
    rollback
  fi
  exit "$status"
}
trap finish 0
trap 'exit 1' INT TERM HUP

compose pull api worker web
compose up -d db
compose run --rm migrate
compose run --rm api verify-config
compose up -d api worker web
domain=$(grep '^DOMAIN=' .env | cut -d= -f2)
if ! curl -fsS --retry 12 --retry-delay 5 "https://$domain/health/ready"; then
  exit 1
fi
compose ps
mv "$candidate" "$current"
deployed=true
