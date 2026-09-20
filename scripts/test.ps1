$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot

function Invoke-GoTests([string]$relativePath, [string]$label) {
  Push-Location (Join-Path $root $relativePath)
  try {
    Write-Host "[$label] go test ./..."
    & go test ./...
    if ($LASTEXITCODE -ne 0) { throw "$label tests failed" }
  } finally {
    Pop-Location
  }
}

Invoke-GoTests 'apps/control-plane' 'control-plane'
Invoke-GoTests 'apps/agent' 'agent'
Invoke-GoTests 'cmd/arkctl' 'arkctl'

Push-Location (Join-Path $root 'tests/contracts')
try {
  Write-Host '[contracts] GOWORK=off go test ./...'
  $previousGoWork = $env:GOWORK
  try {
    $env:GOWORK = 'off'
    & go test ./...
    if ($LASTEXITCODE -ne 0) { throw 'contract tests failed' }
  } finally {
    $env:GOWORK = $previousGoWork
  }
} finally {
  Pop-Location
}

Push-Location (Join-Path $root 'apps/web')
try {
  Write-Host '[web] bun run build.ts'
  & bun run build.ts
  if ($LASTEXITCODE -ne 0) { throw 'web build failed' }
} finally {
  Pop-Location
}

Write-Host '[proxy] websocket reverse-proxy contract'
& (Join-Path $root 'tests/runtime/proxy_contract.ps1')
if ($LASTEXITCODE -ne 0) { throw 'proxy contract failed' }

Write-Host '[acceptance] disposable cleanup contract'
& (Join-Path $root 'tests/runtime/acceptance_cleanup_contract.ps1')
if ($LASTEXITCODE -ne 0) { throw 'acceptance cleanup contract failed' }

Write-Host 'All local test suites passed.'
