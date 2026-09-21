$ErrorActionPreference = 'Stop'

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$webRoot = Join-Path $repoRoot 'apps\web'
$packagePath = Join-Path $webRoot 'package.json'
$dockerfilePath = Join-Path $webRoot 'Dockerfile'
$lockfilePath = Join-Path $webRoot 'bun.lock'

if (-not (Test-Path -LiteralPath $lockfilePath)) {
    throw 'apps/web/bun.lock is required for reproducible web dependencies.'
}

$package = Get-Content -Raw -LiteralPath $packagePath | ConvertFrom-Json
$dependencies = @($package.dependencies.PSObject.Properties)
$latestDependencies = @($dependencies | Where-Object { $_.Value -eq 'latest' })
if ($latestDependencies.Count -gt 0) {
    $names = ($latestDependencies.Name -join ', ')
    throw "Web dependencies must be pinned; found latest: $names"
}

$dockerfile = Get-Content -Raw -LiteralPath $dockerfilePath
if ($dockerfile -notmatch '(?m)^RUN bun install --frozen-lockfile\s*$') {
    throw 'apps/web/Dockerfile must use a standalone frozen Bun install.'
}
if ($dockerfile -match 'bun install\s*\|\|\s*bun install') {
    throw 'apps/web/Dockerfile must not fall back to unconstrained bun install.'
}

Write-Output 'Web dependency reproducibility checks passed.'
