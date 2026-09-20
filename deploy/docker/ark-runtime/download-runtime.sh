#!/usr/bin/env bash
set -Eeuo pipefail

if [[ $# -ne 3 ]]; then
  echo "usage: download-runtime.sh URL DEST SHA256" >&2
  exit 64
fi

url="$1"
destination="$2"
expected_sha256="$3"

if [[ ! "$expected_sha256" =~ ^[[:xdigit:]]{64}$ ]]; then
  echo "a 64-character SHA-256 checksum is required" >&2
  exit 65
fi

mkdir -p "$(dirname "$destination")"
partial="${destination}.part.$$"
cleanup() {
  rm -f -- "$partial"
}
trap cleanup EXIT

curl \
  --fail \
  --location \
  --show-error \
  --progress-bar \
  --retry 3 \
  --retry-all-errors \
  --connect-timeout 30 \
  --max-time 1200 \
  "$url" \
  --output "$partial"

printf '%s  %s\n' "$expected_sha256" "$partial" | sha256sum --check --status
mv -- "$partial" "$destination"
trap - EXIT
echo "verified runtime archive: $destination"
