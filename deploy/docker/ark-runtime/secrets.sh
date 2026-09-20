#!/usr/bin/env bash

read_secret_file() {
  local path="$1"
  local value

  if [[ -z "$path" ]]; then
    return 0
  fi
  if [[ ! -f "$path" ]]; then
    echo "Secret file is missing: $path" >&2
    return 1
  fi
  if [[ ! -r "$path" ]]; then
    echo "Secret file is not readable: $path" >&2
    return 1
  fi

  IFS= read -r value < "$path" || true
  value="${value%$'\r'}"
  printf '%s' "$value"
}
