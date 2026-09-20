#!/usr/bin/env bash
set -Eeuo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

command -v docker >/dev/null 2>&1 || { echo "docker is required" >&2; exit 1; }
docker compose version >/dev/null 2>&1 || { echo "docker compose plugin is required" >&2; exit 1; }

generate_postgres_password() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex 24
  else
    od -An -N24 -tx1 /dev/urandom | tr -d ' \n'
  fi
}

if [[ ! -f .env ]]; then
  cp .env.example .env
  postgres_password="$(generate_postgres_password)"
  sed -i "s/^POSTGRES_PASSWORD=.*/POSTGRES_PASSWORD=$postgres_password/" .env
  chmod 600 .env
  echo "Created .env with a generated PostgreSQL password."
elif grep -q '^POSTGRES_PASSWORD=\(\|change-me\)$' .env; then
  postgres_password="$(generate_postgres_password)"
  sed -i "s/^POSTGRES_PASSWORD=.*/POSTGRES_PASSWORD=$postgres_password/" .env
  chmod 600 .env
  echo "Replaced the unsafe PostgreSQL development password in .env."
fi

docker compose build control-plane agent web
docker compose up -d postgres control-plane socket-proxy agent web
docker compose ps
curl --fail --retry 10 --retry-delay 2 http://127.0.0.1:8080/readyz
