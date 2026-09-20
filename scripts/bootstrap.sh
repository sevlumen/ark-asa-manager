#!/usr/bin/env bash
set -Eeuo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

command -v docker >/dev/null 2>&1 || { echo "docker is required" >&2; exit 1; }
docker compose version >/dev/null 2>&1 || { echo "docker compose plugin is required" >&2; exit 1; }

if [[ ! -f .env ]]; then
  cp .env.example .env
  echo "Created .env; change passwords before exposing the service."
fi

docker compose build control-plane agent web
docker compose up -d postgres control-plane socket-proxy agent web
docker compose ps
curl --fail --retry 10 --retry-delay 2 http://127.0.0.1:8080/readyz
