$ErrorActionPreference = 'Stop'

$root = Resolve-Path (Join-Path $PSScriptRoot '..\..')
$migrationPath = Join-Path $root 'db\migrations\003_port_allocations.sql'
$openapiPath = Join-Path $root 'api\openapi\openapi.yaml'
$failures = [System.Collections.Generic.List[string]]::new()

if (-not (Test-Path $migrationPath)) {
    $failures.Add('Additive port allocation migration 003 is missing.')
} else {
    $migration = Get-Content $migrationPath -Raw
    $upMigration = ($migration -split '-- \+goose Down', 2)[0]
    foreach ($needle in @('port_role', 'game', 'peer', 'query', 'rcon', 'host_port', 'container_port')) {
        if (-not $migration.Contains($needle)) { $failures.Add("Migration must define $needle.") }
    }
    if (-not ($migration.Contains('UNIQUE (node_id, protocol, host_port)') -or $migration.Contains('PRIMARY KEY (node_id, protocol, host_port)'))) {
        $failures.Add('Migration must prevent duplicate host ports per node and protocol.')
    }
    if (-not $migration.Contains('UNIQUE (instance_id, port_role)')) {
        $failures.Add('Migration must allow multiple protocols but one allocation per role.')
    }
    if ($upMigration.Contains('DROP TABLE IF EXISTS port_allocations')) {
        $failures.Add('Migration 003 must not rewrite or drop the existing port allocation table.')
    }
}

$openapi = Get-Content $openapiPath -Raw
foreach ($needle in @('PortAllocation:', 'peer', 'host_port', 'container_port')) {
    if (-not $openapi.Contains($needle)) { $failures.Add("OpenAPI must define $needle.") }
}

if ($failures.Count -gt 0) { throw ($failures -join [Environment]::NewLine) }
Write-Output 'port allocation contract: PASS'
