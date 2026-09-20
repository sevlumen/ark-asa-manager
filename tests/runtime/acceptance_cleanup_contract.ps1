$ErrorActionPreference = 'Stop'

$root = Resolve-Path (Join-Path $PSScriptRoot '..\..')
$scripts = @(
    (Join-Path $root 'tests\acceptance\rbac.ps1'),
    (Join-Path $root 'tests\acceptance\instance_isolation.ps1'),
    (Join-Path $root 'tests\acceptance\instance_delete.ps1'),
    (Join-Path $root 'tests\acceptance\backup_restore.ps1')
)

foreach ($script in $scripts) {
    $content = Get-Content -LiteralPath $script -Raw
    if ($content -notmatch 'docker rm -f') {
        throw "Acceptance cleanup contract failed: $([IO.Path]::GetFileName($script)) must remove the disposable container before removing volumes"
    }
}

Write-Output 'acceptance cleanup contract: PASS'
