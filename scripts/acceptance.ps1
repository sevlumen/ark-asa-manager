$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot

$scripts = @(
    'tests/acceptance/rbac.ps1',
    'tests/acceptance/node_enrollment.ps1',
    'tests/acceptance/instance_isolation.ps1',
    'tests/acceptance/backup_restore.ps1',
    'tests/acceptance/node_delete.ps1',
    'tests/runtime/web_smoke.ps1',
    'tests/runtime/runtime_contract.ps1',
    'tests/runtime/runtime_secrets.ps1'
)

foreach ($relative in $scripts) {
    $path = Join-Path $root $relative
    Write-Output "--- $relative ---"
    & $path
    if ($LASTEXITCODE -ne 0) { throw "Acceptance probe failed: $relative" }
}

Start-Sleep -Seconds 2
$probeVolumes = @(docker volume ls --format '{{.Name}}' | Where-Object { $_ -match 'rbac-probe|isolation-[ab]-|backup-probe|node-delete-probe|remote-enrollment-probe' })
$probeContainers = @(docker ps -a --format '{{.Names}}' | Where-Object { $_ -match '^ark-(rbac-probe|isolation-[ab]-|backup-probe|node-delete-probe|remote-enrollment-probe)' })
if ($probeVolumes.Count -gt 0 -or $probeContainers.Count -gt 0) {
    throw "Acceptance cleanup failed: $($probeVolumes.Count) volumes, $($probeContainers.Count) containers remain."
}

Write-Output 'acceptance matrix: PASS (direct, clean)'
