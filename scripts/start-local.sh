#!/bin/sh
set -eu

cd "$(dirname "$0")/.."

if [ ! -f .env ]; then
  cp .env.example .env
fi

gen_secret() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -base64 48
    return
  fi
  if command -v python3 >/dev/null 2>&1; then
    python3 - <<'PY'
import base64, os
print(base64.b64encode(os.urandom(48)).decode())
PY
    return
  fi
  dd if=/dev/urandom bs=48 count=1 2>/dev/null | base64
}

current_secret=$(grep '^COOKIE_SECRET=' .env 2>/dev/null | head -n 1 | cut -d= -f2- || true)
if [ -z "$current_secret" ] || [ "$current_secret" = "dev-change-me-32-bytes-minimum" ]; then
  secret=$(gen_secret)
  tmp=$(mktemp)
  if grep -q '^COOKIE_SECRET=' .env; then
    sed "s|^COOKIE_SECRET=.*|COOKIE_SECRET=$secret|" .env > "$tmp"
  else
    cat .env > "$tmp"
    printf '\nCOOKIE_SECRET=%s\n' "$secret" >> "$tmp"
  fi
  mv "$tmp" .env
  echo "Generated COOKIE_SECRET in .env"
fi

mkdir -p storage/source storage/hls bin

if ! command -v ffmpeg >/dev/null 2>&1; then
  echo "warning: ffmpeg not found on PATH" >&2
fi
if ! command -v ffprobe >/dev/null 2>&1; then
  echo "warning: ffprobe not found on PATH" >&2
fi

make deps
make build
exec ./bin/drifter serve
