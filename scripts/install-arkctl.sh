#!/usr/bin/env bash
set -Eeuo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
mkdir -p "$repo_root/.bin"
docker run --rm -v "$repo_root:/src" -w /src golang:1.23-alpine \
  go build -trimpath -ldflags='-s -w' -o /src/.bin/arkctl ./cmd/arkctl
echo "Built $repo_root/.bin/arkctl"
