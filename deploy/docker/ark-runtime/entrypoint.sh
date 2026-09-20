#!/usr/bin/env bash
set -Eeuo pipefail

source /usr/local/bin/secrets.sh

game_root=/opt/ark/game
data_root=/opt/ark/data
server_exe="$game_root/ShooterGame/Binaries/Win64/ArkAscendedServer.exe"
server_log="$data_root/log/current-server.log"
engine_log="$data_root/log/ShooterGame.log"
ready_marker="$data_root/log/ready.marker"
server_pid=""
xvfb_pid=""

if [[ -n "${ARK_ADMIN_PASSWORD:-}" || -n "${ARK_SERVER_PASSWORD:-}" ]]; then
  echo 'Plaintext ARK passwords are not accepted; configure *_PASSWORD_FILE instead.' >&2
  exit 1
fi

rcon_enabled="${ARK_RCON_ENABLED:-false}"
case "${rcon_enabled,,}" in
  true) rcon_enabled=true ;;
  false) rcon_enabled=false ;;
  *)
    echo 'ARK_RCON_ENABLED must be true or false.' >&2
    exit 1
    ;;
esac

secret_values=()
server_password=""
admin_password=""

if [[ -n "${ARK_SERVER_PASSWORD_FILE:-}" ]]; then
  server_password="$(read_secret_file "$ARK_SERVER_PASSWORD_FILE")"
  [[ -z "$server_password" ]] || secret_values+=("$server_password")
fi
if [[ -n "${ARK_ADMIN_PASSWORD_FILE:-}" ]]; then
  admin_password="$(read_secret_file "$ARK_ADMIN_PASSWORD_FILE")"
  [[ -z "$admin_password" ]] || secret_values+=("$admin_password")
fi
if [[ "$rcon_enabled" == true && -z "$admin_password" ]]; then
  echo 'ARK_ADMIN_PASSWORD_FILE is required and must contain a value when RCON is enabled.' >&2
  exit 1
fi
if [[ -n "${STEAM_PASSWORD:-}" ]]; then
  secret_values+=("$STEAM_PASSWORD")
fi

mkdir -p "$data_root"/{save,config,log,backups,cluster}
rm -f "$ready_marker"
: > "$server_log"

if [[ "${ARK_UPDATE_ON_START:-true}" == "true" ]]; then
  steam_args=(+force_install_dir "$game_root" +login "${STEAM_USER:-anonymous}")
  if [[ -n "${STEAM_PASSWORD:-}" ]]; then
    steam_args+=("$STEAM_PASSWORD")
  fi
  steam_args+=(+app_update 2430930 validate +quit)
  steam_binary=/opt/ark/linux32/steamcmd
  steam_log="$data_root/log/steamcmd.log"
  export LD_LIBRARY_PATH="/opt/ark/linux32:${LD_LIBRARY_PATH:-}"
  steam_installed=0

  for steam_attempt in 1 2 3 4 5; do
    echo "SteamCMD app update attempt $steam_attempt/5" | tee -a "$steam_log"
    set +e
    "$steam_binary" "${steam_args[@]}" 2>&1 | redact_stream | tee -a "$steam_log"
    steam_status=${PIPESTATUS[0]}
    set -e

    if [[ -f "$server_exe" ]]; then
      steam_installed=1
      break
    fi

    if [[ "$steam_status" != "0" && "$steam_status" != "42" ]]; then
      echo "SteamCMD failed with exit code $steam_status" >&2
      break
    fi
    sleep 2
  done

  if [[ "$steam_installed" != "1" ]]; then
    echo "SteamCMD did not install ARK app 2430930: $server_exe" >&2
    exit 1
  fi
fi

if [[ ! -f "$server_exe" ]]; then
  echo "ARK server executable was not installed: $server_exe" >&2
  exit 1
fi

sentry_plugin="$game_root/ShooterGame/Plugins/sentry"
if [[ "${ARK_DISABLE_SENTRY:-true}" == "true" && -d "$sentry_plugin" ]]; then
  if [[ ! -e "${sentry_plugin}.disabled" ]]; then
    mv "$sentry_plugin" "${sentry_plugin}.disabled"
  else
    mv "$sentry_plugin" "${sentry_plugin}.disabled.$(date +%s)"
  fi
fi

steam_sdk_dir="${HOME:-/home/ark}/.steam/sdk64"
mkdir -p "$steam_sdk_dir"
for native_steamclient in "$game_root/linux64/steamclient.so" "$game_root/steamclient.so"; do
  if [[ -f "$native_steamclient" ]]; then
    rm -f "$steam_sdk_dir/steamclient.so"
    ln -s "$native_steamclient" "$steam_sdk_dir/steamclient.so"
    break
  fi
done

mkdir -p "$game_root/ShooterGame/Saved"
for item in Config Logs Saved; do
  target="$data_root/config"
  [[ "$item" == "Logs" ]] && target="$data_root/log"
  [[ "$item" == "Saved" ]] && target="$data_root/save"
  rm -rf "$game_root/ShooterGame/Saved/$item"
  mkdir -p "$target"
  ln -s "$target" "$game_root/ShooterGame/Saved/$item"
done

mkdir -p "$data_root/config/WindowsServer"
write_ark_passwords_config \
  "$data_root/config/WindowsServer/GameUserSettings.ini" \
  "$admin_password" \
  "$server_password"

proton_data_root="${STEAM_COMPAT_DATA_PATH:-$data_root/config/proton}"
export STEAM_COMPAT_DATA_PATH="$proton_data_root"
export STEAM_COMPAT_CLIENT_INSTALL_PATH="${STEAM_COMPAT_CLIENT_INSTALL_PATH:-/opt/ark}"
export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-$data_root/config/runtime}"
export PROTON_USE_XALIA="${PROTON_USE_XALIA:-0}"
export PROTON_USE_WINED3D="${PROTON_USE_WINED3D:-1}"
export WINEDLLOVERRIDES="${WINEDLLOVERRIDES:-version=n,b}"
export UMU_ID="${UMU_ID:-2430930}"
export SteamAppId="${SteamAppId:-2430930}"
export SteamGameId="${SteamGameId:-2430930}"
export STEAM_COMPAT_APP_ID="${STEAM_COMPAT_APP_ID:-2430930}"
export WINEPREFIX="${WINEPREFIX:-$proton_data_root/pfx}"
mkdir -p "$STEAM_COMPAT_DATA_PATH" "$XDG_RUNTIME_DIR"
mkdir -p "$WINEPREFIX/dosdevices"
chmod 700 "$XDG_RUNTIME_DIR"

if [[ "${ARK_XVFB:-true}" == "true" ]]; then
  export DISPLAY="${DISPLAY:-:99}"
  mkdir -p /tmp/.X11-unix
  Xvfb "$DISPLAY" -screen 0 "${ARK_XVFB_SCREEN:-1024x768x16}" -nolisten tcp > "$data_root/log/xvfb.log" 2>&1 &
  xvfb_pid=$!
  sleep 1
  if ! kill -0 "$xvfb_pid" 2>/dev/null; then
    echo "Xvfb failed to start on $DISPLAY" >&2
    exit 1
  fi
fi

if [[ "${ARK_INSTALL_VCREDIST:-true}" == "true" && ! -f "$WINEPREFIX/drive_c/windows/system32/vcruntime140.dll" ]]; then
  echo "Installing Microsoft Visual C++ runtime into the persistent Proton prefix" | tee -a "$server_log"
  if [[ ! -f "$WINEPREFIX/drive_c/windows/system32/kernel32.dll" ]]; then
    /opt/ark/proton/proton run /opt/ark/vc_redist.x64.exe /quiet /norestart 2>&1 | redact_stream | tee -a "$server_log"
  else
    /opt/ark/proton/proton runinprefix /opt/ark/vc_redist.x64.exe /quiet /norestart 2>&1 | redact_stream | tee -a "$server_log"
  fi
fi

pve_query='ServerPVE=true'
if [[ "${ARK_PVE:-true}" != "true" ]]; then
  pve_query='ServerPVE=false'
fi

server_name="${ARK_SERVER_NAME:-ARK_ASA_TheIsland}"
server_name="${server_name// /_}"

server_query="${ARK_MAP:-TheIsland_WP}?listen?SessionName=${server_name}?MaxPlayers=${ARK_MAX_PLAYERS:-10}?${pve_query}?RCONEnabled=${rcon_enabled}"
if [[ "$rcon_enabled" == true ]]; then
  server_query+="?RCONPort=${ARK_RCON_PORT:-32330}"
fi

server_args=(
  "$server_query"
  -game
  -server
  -log
  "-WinLiveMaxPlayers=${ARK_MAX_PLAYERS:-10}"
  "-Port=${ARK_PORT:-7777}"
  "-QueryPort=${ARK_QUERY_PORT:-27015}"
  "-clusterid=${ARK_CLUSTER_ID:-local-cluster}"
  "-ClusterDirOverride=$data_root/cluster"
  -NoTransferFromFiltering
  -NoHangDetection
)

if [[ "${ARK_CROSSPLAY:-true}" == "true" ]]; then
  server_args+=(-crossplay)
fi
if [[ "${ARK_NO_STEAM_CLIENT:-true}" == "true" ]]; then
  server_args+=(-nosteamclient)
fi
if [[ "${ARK_DX11:-true}" == "true" ]]; then
  server_args+=(-dx11)
fi
if [[ "${ARK_BATTLEYE:-true}" != "true" ]]; then
  server_args+=(-NoBattlEye)
fi
if [[ "${ARK_DISABLE_GAME_ANALYTICS:-true}" == "true" ]]; then
  server_args+=(-NoGameAnalytics)
fi

(
  cd "$game_root/ShooterGame/Binaries/Win64"
  /opt/ark/proton/proton run ./ArkAscendedServer.exe "${server_args[@]}" 2>&1 | redact_stream | tee -a "$server_log"
) &
server_pid=$!

cleanup() {
  if [[ -n "$server_pid" ]] && kill -0 "$server_pid" 2>/dev/null; then
    kill "$server_pid" 2>/dev/null || true
  fi
  if [[ -n "$xvfb_pid" ]] && kill -0 "$xvfb_pid" 2>/dev/null; then
    kill "$xvfb_pid" 2>/dev/null || true
  fi
}
trap cleanup INT TERM EXIT

while kill -0 "$server_pid" 2>/dev/null; do
  if grep -Eiq 'loaded map|server is ready|full startup|advertising for join|successfully started' "$engine_log" 2>/dev/null; then
    date -u +%Y-%m-%dT%H:%M:%SZ > "$ready_marker"
    break
  fi
  sleep 2
done

set +e
wait "$server_pid"
server_status=$?
set -e
rm -f "$ready_marker"
exit "$server_status"
