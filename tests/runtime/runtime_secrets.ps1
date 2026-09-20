$ErrorActionPreference = 'Stop'

$root = Resolve-Path (Join-Path $PSScriptRoot '..\..')
$compose = Get-Content (Join-Path $root 'compose.yml') -Raw
$rconCompose = Get-Content (Join-Path $root 'compose.rcon.yml') -Raw
$secretCompose = Get-Content (Join-Path $root 'compose.secrets.yml.example') -Raw
$runtimeEnv = Get-Content (Join-Path $root 'deploy\docker\ark-runtime\runtime.env.example') -Raw
$entrypoint = Get-Content (Join-Path $root 'deploy\docker\ark-runtime\entrypoint.sh') -Raw
$secrets = Join-Path $root 'deploy\docker\ark-runtime\secrets.sh'
$failures = [System.Collections.Generic.List[string]]::new()

function Assert-Contains([string]$Text, [string]$Needle, [string]$Message) {
    if (-not $Text.Contains($Needle)) { $failures.Add($Message) }
}

function Assert-NotContains([string]$Text, [string]$Needle, [string]$Message) {
    if ($Text.Contains($Needle)) { $failures.Add($Message) }
}

if (-not (Test-Path $secrets)) {
    $failures.Add('Runtime secret reader is missing.')
} else {
    $secretReader = Get-Content $secrets -Raw
    Assert-Contains $secretReader 'read_secret_file' 'Runtime secret reader must expose read_secret_file.'
    Assert-Contains $secretReader 'write_ark_passwords_config' 'Runtime secret helper must write passwords to GameUserSettings.ini.'
    Assert-Contains $secretReader 'file is missing' 'Missing secret files must fail with a non-secret diagnostic.'
    Assert-NotContains $secretReader 'printf.*secret' 'Secret reader must not print secret values.'
}

Assert-Contains $compose 'ARK_RCON_ENABLED' 'Compose must make RCON enablement explicit.'
Assert-Contains $compose 'ARK_ADMIN_PASSWORD_FILE' 'Compose must pass the admin password file path.'
Assert-Contains $compose 'ARK_SERVER_PASSWORD_FILE' 'Compose must pass the optional server password file path.'
Assert-NotContains $compose 'ARK_ADMIN_PASSWORD: ${ARK_ADMIN_PASSWORD:-' 'Compose must not provide a plaintext admin password default.'
Assert-NotContains $compose 'ARK_RCON_PORT:-32330}:${ARK_RCON_PORT:-32330}/tcp' 'Base Compose must not publish RCON unconditionally.'
Assert-Contains $rconCompose 'ARK_RCON_ENABLED: "true"' 'RCON override must enable RCON explicitly.'
Assert-Contains $rconCompose 'ARK_RCON_PORT:-32330}:${ARK_RCON_PORT:-32330}/tcp' 'RCON override must publish the configured RCON port.'
Assert-Contains $rconCompose 'target: /run/secrets/ark_admin_password' 'RCON override must mount the admin secret into the container.'
Assert-Contains $rconCompose 'read_only: true' 'RCON override must mount the admin secret read-only.'
Assert-Contains $secretCompose 'ARK_SERVER_PASSWORD_SECRET_FILE' 'Secret override must support the optional server password file.'
Assert-Contains $secretCompose 'target: /run/secrets/ark_server_password' 'Secret override must mount the server secret into the container.'

Assert-Contains $runtimeEnv 'ARK_RCON_ENABLED=false' 'Runtime example must default RCON to disabled explicitly.'
Assert-Contains $runtimeEnv 'ARK_ADMIN_PASSWORD_FILE=' 'Runtime example must document the admin password file.'
Assert-Contains $runtimeEnv 'ARK_SERVER_PASSWORD_FILE=' 'Runtime example must document the optional server password file.'
Assert-NotContains $runtimeEnv 'ARK_ADMIN_PASSWORD=change-admin-password' 'Runtime example must not contain a plaintext admin password.'

Assert-Contains $entrypoint 'ARK_ADMIN_PASSWORD_FILE' 'Entrypoint must read the admin password from a file.'
Assert-Contains $entrypoint 'ARK_SERVER_PASSWORD_FILE' 'Entrypoint must read the server password from a file.'
Assert-Contains $entrypoint 'config/WindowsServer/GameUserSettings.ini' 'Entrypoint must configure passwords through the Proton ASA WindowsServer GameUserSettings.ini.'
Assert-NotContains $entrypoint 'server_query+="?ServerAdminPassword=' 'Entrypoint must not expose the admin password in process argv.'
Assert-NotContains $entrypoint 'server_query+="?ServerPassword=' 'Entrypoint must not expose the server password in process argv.'
Assert-Contains $secretReader 'ServerAdminPassword=' 'Secret helper must write ServerAdminPassword to the runtime config.'
Assert-Contains $entrypoint 'RCONEnabled=' 'Entrypoint must derive RCONEnabled from explicit configuration.'
Assert-Contains $entrypoint 'redact_stream' 'Runtime output must redact loaded secret values.'
Assert-NotContains $entrypoint 'ServerPassword=${ARK_SERVER_PASSWORD:-}' 'Entrypoint must not use a plaintext server password fallback.'
Assert-NotContains $entrypoint '?RCONEnabled=True?RCONPort=' 'Entrypoint must not force-enable RCON.'

$image = 'ark-asa-runtime:local'
if (docker image inspect $image 2>$null) {
    # The running Compose service may still use the previous image digest while
    # a rebuilt local tag is waiting for an intentional redeploy. Use the
    # Compose label directly so this probe does not parse compose.yml or require
    # unrelated environment such as POSTGRES_PASSWORD.
    $runtimeContainer = docker ps --filter 'label=com.docker.compose.service=ark' --filter 'status=running' --format '{{.ID}}' | Select-Object -First 1
    if (-not $runtimeContainer) {
        $runtimeContainer = docker ps --filter "ancestor=$image" --filter 'status=running' --format '{{.ID}}' | Select-Object -First 1
    }
    if (-not $runtimeContainer) {
        $failures.Add('Runtime secret probes require a running container for the inspected image.')
    }
    function Invoke-DockerBashScript([string]$Script) {
        $bytes = [Text.Encoding]::UTF8.GetBytes(($Script -replace "`r", ''))
        $encoded = [Convert]::ToBase64String($bytes)
        docker exec $runtimeContainer bash -c "echo $encoded | base64 -d | bash -s"
    }

    $configProbe = @'
source /usr/local/bin/secrets.sh
config=$(mktemp)
echo '[ServerSettings]' > "$config"
echo 'ServerName=SmokeTest' >> "$config"
echo 'ServerAdminPassword=old-admin-password' >> "$config"
write_ark_passwords_config "$config" "fake-admin-password" "fake-server-password"
grep -Fqx 'ServerAdminPassword=fake-admin-password' "$config"
grep -Fqx 'ServerPassword=fake-server-password' "$config"
! grep -Fqx 'ServerAdminPassword=old-admin-password' "$config"
test "$(stat -c %a "$config")" = 600
write_ark_passwords_config "$config" "" ""
! grep -q '^ServerAdminPassword=' "$config"
! grep -q '^ServerPassword=' "$config"
'@
    Invoke-DockerBashScript $configProbe
    if ($LASTEXITCODE -ne 0) {
        $failures.Add('Runtime config probe must write file-backed passwords with restrictive permissions.')
    }

    $probe = @'
source /usr/local/bin/secrets.sh
secret_values=(fake-runtime-secret fake-server-password fake-steam-password)
cat <<'LOG' | redact_stream
SteamCMD login fake-steam-password app_update 2430930
ARK launch args ServerAdminPassword=fake-runtime-secret ServerPassword=fake-server-password
RCON connection password=fake-runtime-secret
LOG
'@
    $redacted = Invoke-DockerBashScript $probe
    if ($LASTEXITCODE -ne 0 -or $redacted -match 'fake-runtime-secret|fake-server-password|fake-steam-password' -or ($redacted -split "`r?`n").Count -lt 3) {
        $failures.Add('Runtime image redaction probe must hide fake secrets across all representative runtime log lines.')
    }
}

if ($failures.Count -gt 0) {
    throw ($failures -join [Environment]::NewLine)
}

Write-Output 'runtime secrets contract: PASS'
