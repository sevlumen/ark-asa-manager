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

write_ark_passwords_config() {
  local config_file="$1"
  local admin_password="$2"
  local server_password="$3"
  local temp_file="${config_file}.tmp.$$"
  local line
  local in_server_settings=0
  local saw_server_settings=0
  local admin_written=0
  local server_written=0

  emit_missing_passwords() {
    if [[ -n "$admin_password" && "$admin_written" == 0 ]]; then
      printf 'ServerAdminPassword=%s\n' "$admin_password"
      admin_written=1
    fi
    if [[ -n "$server_password" && "$server_written" == 0 ]]; then
      printf 'ServerPassword=%s\n' "$server_password"
      server_written=1
    fi
  }

  : > "$temp_file"
  if [[ -f "$config_file" ]]; then
    while IFS= read -r line || [[ -n "$line" ]]; do
      if [[ "$line" == '[ServerSettings]' ]]; then
        if [[ "$in_server_settings" == 1 ]]; then
          emit_missing_passwords
        fi
        in_server_settings=1
        saw_server_settings=1
        printf '%s\n' "$line"
      elif [[ "$line" == \[*\] ]]; then
        if [[ "$in_server_settings" == 1 ]]; then
          emit_missing_passwords
          in_server_settings=0
        fi
        printf '%s\n' "$line"
      elif [[ "$in_server_settings" == 1 && "$line" == ServerAdminPassword=* ]]; then
        if [[ -n "$admin_password" ]]; then
          printf 'ServerAdminPassword=%s\n' "$admin_password"
          admin_written=1
        fi
      elif [[ "$in_server_settings" == 1 && "$line" == ServerPassword=* ]]; then
        if [[ -n "$server_password" ]]; then
          printf 'ServerPassword=%s\n' "$server_password"
          server_written=1
        fi
      else
        printf '%s\n' "$line"
      fi
    done < "$config_file" > "$temp_file"
  fi

  if [[ "$in_server_settings" == 1 ]]; then
    emit_missing_passwords >> "$temp_file"
  elif [[ "$saw_server_settings" == 0 ]]; then
    {
      printf '[ServerSettings]\n'
      emit_missing_passwords
    } >> "$temp_file"
  fi

  chmod 600 "$temp_file"
  mv -f "$temp_file" "$config_file"
}

redact_stream() {
  local line
  local secret

  while IFS= read -r line || [[ -n "$line" ]]; do
    for secret in "${secret_values[@]-}"; do
      [[ -z "$secret" ]] || line="${line//"$secret"/[REDACTED]}"
    done
    printf '%s\n' "$line"
  done
}
