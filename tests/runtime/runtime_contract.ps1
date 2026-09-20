$ErrorActionPreference = 'Stop'

$root = Resolve-Path (Join-Path $PSScriptRoot '..\..')
$dockerfile = Get-Content (Join-Path $root 'deploy\docker\ark-runtime\Dockerfile') -Raw
$downloader = Get-Content (Join-Path $root 'deploy\docker\ark-runtime\download-runtime.sh') -Raw -ErrorAction SilentlyContinue
$entrypoint = Get-Content (Join-Path $root 'deploy\docker\ark-runtime\entrypoint.sh') -Raw
$healthcheck = Get-Content (Join-Path $root 'deploy\docker\ark-runtime\healthcheck.sh') -Raw
$compose = Get-Content (Join-Path $root 'compose.yml') -Raw
$webPackage = Get-Content (Join-Path $root 'apps\web\package.json') -Raw
$webDockerfile = Get-Content (Join-Path $root 'apps\web\Dockerfile') -Raw
$envExample = Get-Content (Join-Path $root '.env.example') -Raw
$bootstrap = Get-Content (Join-Path $root 'scripts\bootstrap.sh') -Raw
$bootstrapPs = Get-Content (Join-Path $root 'scripts\bootstrap.ps1') -Raw
$failures = [System.Collections.Generic.List[string]]::new()

function Assert-Contains([string]$Text, [string]$Needle, [string]$Message) {
    if (-not $Text.Contains($Needle)) { $failures.Add($Message) }
}

function Assert-NotContains([string]$Text, [string]$Needle, [string]$Message) {
    if ($Text.Contains($Needle)) { $failures.Add($Message) }
}

Assert-Contains $dockerfile 'ARG PROTON_SHA256=' 'Proton checksum build argument is missing.'
Assert-Contains $dockerfile 'ARG STEAMCMD_SHA256=' 'SteamCMD checksum build argument is missing.'
Assert-Contains $dockerfile 'ARG PROTON_VERSION=GE-Proton10-34' 'Runtime must use the tested GE-Proton version.'
Assert-Contains $dockerfile 'download-runtime.sh' 'Runtime downloader with bounded retries is missing.'
Assert-Contains $downloader '--progress-bar' 'Runtime download must expose progress in Docker build logs.'
Assert-Contains $dockerfile 'USER ark' 'Runtime image must run as the non-root ark user.'
Assert-Contains $dockerfile '--uid 10001' 'Runtime image must use a stable non-root UID.'
Assert-Contains $dockerfile 'python3' 'SteamCMD post-install scripts require Python 3 in the runtime image.'
Assert-Contains $dockerfile 'libfreetype6' 'Proton requires FreeType runtime libraries.'
Assert-Contains $dockerfile 'libfontconfig1' 'Proton requires fontconfig runtime libraries.'
Assert-Contains $dockerfile 'dpkg --add-architecture i386' 'Wine 32-bit dependencies require i386 multiarch.'
Assert-Contains $dockerfile 'libfreetype6:i386' 'Wine 32-bit requires the i386 FreeType library.'
Assert-Contains $dockerfile 'libfontconfig1:i386' 'Wine 32-bit requires the i386 fontconfig library.'
Assert-Contains $dockerfile 'xvfb' 'Headless Proton execution requires a virtual X display.'
Assert-Contains $dockerfile 'x11-xserver-utils' 'Headless X11 utilities are required for the virtual display.'
Assert-Contains $dockerfile 'libvulkan1:i386' 'Proton requires the i386 Vulkan loader.'
Assert-Contains $dockerfile 'libopenal1:i386' 'ASA audio initialization requires the i386 OpenAL library.'
Assert-Contains $dockerfile 'dbus-uuidgen --ensure=/etc/machine-id' 'Proton requires a valid machine-id.'
Assert-Contains $dockerfile 'VC_REDIST_SHA256=' 'The Microsoft VC++ redistributable must be checksum pinned.'
Assert-Contains $dockerfile 'vc_redist.x64.exe' 'The runtime image must carry the Microsoft VC++ redistributable installer.'
Assert-Contains $downloader 'sha256sum' 'Runtime archives must be checksum verified.'
Assert-Contains $downloader '--retry 3' 'Runtime downloads must retry three times.'
Assert-Contains $entrypoint 'TheIsland_WP' 'TheIsland_WP must be the default map.'
Assert-Contains $entrypoint 'ServerPVE=true' 'PvE must be enabled by default.'
Assert-Contains $entrypoint '-crossplay' 'Crossplay must be enabled.'
Assert-Contains $entrypoint 'ARK_PVE' 'PvE must be configurable.'
Assert-Contains $entrypoint 'ARK_CROSSPLAY' 'Crossplay must be configurable.'
Assert-Contains $entrypoint 'ARK_BATTLEYE' 'BattlEye must be configurable.'
Assert-Contains $entrypoint 'NoGameAnalytics' 'Game Analytics must be disableable for dedicated runtime deployments.'
Assert-Contains $entrypoint 'NoBattlEye' 'BattlEye must be enabled unless explicitly disabled.'
Assert-Contains $entrypoint 'if [[ "${ARK_UPDATE_ON_START:-true}" == "true" ]]; then' 'ARK_UPDATE_ON_START=false must not install missing game files.'
Assert-Contains $entrypoint 'STEAM_COMPAT_DATA_PATH' 'Proton must have a persistent compat data path.'
Assert-Contains $entrypoint 'UMU_ID' 'Proton must launch the non-Steam ASA executable directly.'
Assert-Contains $entrypoint 'SteamAppId' 'ASA must expose its Steam app identity to Proton.'
Assert-Contains $entrypoint 'STEAM_COMPAT_APP_ID' 'Proton must receive the ASA compat app identity.'
Assert-Contains $entrypoint 'PROTON_USE_XALIA' 'Headless Proton must disable Xalia.'
Assert-Contains $entrypoint 'DISPLAY' 'Headless Proton must export a virtual display.'
Assert-Contains $entrypoint 'Xvfb' 'The entrypoint must start the virtual display before ASA.'
Assert-Contains $entrypoint 'WINEDLLOVERRIDES' 'The ASA Proton launch must configure the Windows DLL overrides.'
Assert-Contains $entrypoint '-game' 'The ASA server launch must include the game mode flag.'
Assert-Contains $entrypoint 'ARK_INSTALL_VCREDIST' 'VC++ redistributable installation must be configurable.'
Assert-Contains $entrypoint 'proton run /opt/ark/vc_redist.x64.exe' 'VC++ installation must bootstrap a missing Proton prefix.'
Assert-Contains $entrypoint 'write_ark_passwords_config' 'ARK passwords must be written to the runtime config instead of process argv.'
Assert-Contains $entrypoint 'kill -TERM "$server_pid"' 'Runtime must request graceful shutdown before force termination.'
Assert-Contains $entrypoint 'kill -KILL "$server_pid"' 'Runtime must have a bounded force-termination fallback.'
Assert-Contains $entrypoint 'ARK_GRACEFUL_SHUTDOWN_SECONDS must be a positive integer.' 'Runtime must validate the graceful shutdown timeout.'
Assert-Contains $compose 'stop_grace_period: 45s' 'ARK Compose service must allow graceful shutdown to complete.'
Assert-Contains $compose 'ARK_GRACEFUL_SHUTDOWN_SECONDS' 'Compose must expose the graceful shutdown timeout.'
Assert-Contains $entrypoint 'config/WindowsServer/GameUserSettings.ini' 'ARK password config must target the Proton ASA WindowsServer config path.'
Assert-NotContains $entrypoint 'server_query+="?ServerAdminPassword=' 'Admin password must not be passed through the server process argv.'
Assert-NotContains $entrypoint 'server_query+="?ServerPassword=' 'Server password must not be passed through the server process argv.'
Assert-Contains $entrypoint 'runinprefix' 'The VC++ redistributable must be installed into the persistent Proton prefix.'
Assert-Contains $entrypoint 'mkdir -p "$WINEPREFIX/dosdevices"' 'The Proton prefix drive mapping directory must exist before runinprefix.'
Assert-Contains $entrypoint 'vcruntime140.dll' 'VC++ installation must be idempotent against the persistent prefix.'
Assert-Contains $entrypoint 'sdk64' 'Proton must have a native Steam SDK search directory.'
Assert-Contains $entrypoint 'steamclient.so' 'The native Steam client library must be linked for Proton.'
Assert-Contains $entrypoint 'linux64/steamclient.so' 'Proton must prefer the 64-bit native Steam client library for sdk64.'
Assert-Contains $entrypoint 'ARK_NO_STEAM_CLIENT' 'The Proton server launch must allow disabling the native Steam client.'
Assert-Contains $entrypoint '-nosteamclient' 'The headless ASA launch must support the no-Steam-client mode.'
Assert-Contains $entrypoint 'PROTON_USE_WINED3D' 'Headless Proton must support the software rendering fallback.'
Assert-Contains $entrypoint '-dx11' 'The headless ASA launch must support the DX11 rendering fallback.'
Assert-Contains $entrypoint 'ARK_DISABLE_SENTRY' 'Wine compatibility workaround for Sentry must be configurable.'
Assert-Contains $entrypoint 'sentry_plugin}.disabled' 'The Sentry crashpad workaround must be reversible.'
Assert-Contains $healthcheck 'ready.marker' 'Healthcheck must require the real readiness marker.'
Assert-Contains $healthcheck 'ArkAscendedServer.exe' 'Healthcheck must require the real server process.'
Assert-Contains $compose 'ark-game:/opt/ark/game' 'Game volume must be separate.'
Assert-Contains $compose 'ark-save:/opt/ark/data/save' 'Save volume must be separate.'
Assert-Contains $compose 'ark-config:/opt/ark/data/config' 'Config volume must be separate.'
Assert-Contains $compose 'ark-logs:/opt/ark/data/log' 'Log volume must be separate.'
Assert-Contains $compose 'ark-backups:/opt/ark/data/backups' 'Backup volume must be separate.'
Assert-Contains $compose 'ark-cluster:/opt/ark/data/cluster' 'Cluster volume must be separate.'
Assert-Contains $compose 'ark-permissions' 'Named runtime volumes need a Docker-only ownership init service.'
Assert-Contains $compose '10001:10001' 'Runtime volume ownership must target the non-root UID.'
Assert-Contains $compose 'max-size' 'Runtime logs must have rotation limits.'
Assert-Contains $compose 'ARK_MEMORY_LIMIT' 'Runtime memory limit must be configurable.'
Assert-Contains $compose 'ARK_START_PERIOD' 'Runtime start period must cover first game installation.'
Assert-Contains $compose 'ARK_DISABLE_GAME_ANALYTICS' 'Compose must expose the Game Analytics setting.'
Assert-Contains $compose 'seccomp=unconfined' 'umu pressure-vessel requires an unconfined seccomp profile.'
Assert-Contains $compose 'apparmor=unconfined' 'umu pressure-vessel requires an unconfined AppArmor profile.'
Assert-NotContains $webPackage '"latest"' 'Web dependencies must be pinned to exact versions.'
Assert-Contains $webDockerfile 'bun install --frozen-lockfile' 'Web image must use a frozen dependency install.'
Assert-NotContains $webDockerfile '|| bun install' 'Web image must not fall back to a non-frozen install.'
Assert-Contains $webDockerfile 'oven/bun:1.2-alpine@sha256:' 'Web build and runtime images must be digest pinned.'
Assert-NotContains $compose 'tecnativa/docker-socket-proxy:latest' 'Socket proxy image must not float on latest.'
Assert-NotContains $compose 'POSTGRES_PASSWORD:-change-me' 'Compose must not provide the unsafe PostgreSQL password fallback.'
Assert-NotContains $envExample 'POSTGRES_PASSWORD=change-me' 'Environment example must not contain the unsafe PostgreSQL password.'
Assert-Contains $compose 'POSTGRES_PASSWORD:?' 'Compose must require an explicit PostgreSQL password.'
Assert-Contains $bootstrap 'openssl rand -hex 24' 'Bootstrap must generate a PostgreSQL password when creating .env.'
Assert-Contains $bootstrapPs 'RandomNumberGenerator' 'PowerShell bootstrap must generate a PostgreSQL password with a CSPRNG.'
Assert-Contains $bootstrapPs 'BitConverter' 'PowerShell bootstrap must support Windows PowerShell password generation.'
Assert-Contains $bootstrapPs 'Set-Acl' 'PowerShell bootstrap must protect the local .env file.'
Assert-Contains $compose 'PUBLIC_ORIGIN:-http://localhost:3000' 'Control-plane origin must match the web console default.'
Assert-Contains $envExample 'PUBLIC_ORIGIN=http://localhost:3000' 'Environment example must document the web console origin.'
Assert-Contains $compose 'control-plane-enrollment-ca' 'Enrollment CA must use a dedicated Docker volume.'
Assert-Contains $compose 'ENROLLMENT_CA_CERT_FILE' 'Control-plane enrollment signer certificate path is missing.'
Assert-Contains $compose 'AGENT_ENROLLMENT_CA_FILE' 'Agent enrollment CA trust path is missing.'

if ($failures.Count -gt 0) {
    throw ($failures -join [Environment]::NewLine)
}

Write-Output 'runtime contract: PASS'
