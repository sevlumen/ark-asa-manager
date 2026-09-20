$ErrorActionPreference = 'Stop'

$root = Resolve-Path (Join-Path $PSScriptRoot '..\..')
$container = "ark-port-down-guard-$PID"

function Invoke-Psql([string]$Sql) {
    $output = $Sql | docker exec --interactive $container psql -v ON_ERROR_STOP=1 -U postgres -d postgres 2>&1
    $exitCode = $LASTEXITCODE
    [pscustomobject]@{ Output = ($output -join [Environment]::NewLine); ExitCode = $exitCode }
}

docker rm --force $container 2>$null | Out-Null
docker run --detach --name $container --env POSTGRES_PASSWORD=postgres postgres:16-alpine | Out-Null

try {
    $ready = $false
    for ($attempt = 0; $attempt -lt 60; $attempt++) {
        docker exec $container pg_isready -U postgres 2>$null | Out-Null
        if ($LASTEXITCODE -eq 0) {
            $ready = $true
            break
        }
        Start-Sleep -Seconds 1
    }
    if (-not $ready) { throw 'PostgreSQL did not become ready for the down-guard test.' }

    $migration1 = (Get-Content (Join-Path $root 'db\migrations\001_init.sql') -Raw) -split '-- \+goose Down', 2
    $migration2 = (Get-Content (Join-Path $root 'db\migrations\002_platform.sql') -Raw) -split '-- \+goose Down', 2
    $migration3 = (Get-Content (Join-Path $root 'db\migrations\003_port_allocations.sql') -Raw) -split '-- \+goose Down', 2

    foreach ($sql in @($migration1[0], $migration2[0])) {
        $result = Invoke-Psql $sql
        if ($result.ExitCode -ne 0) { throw $result.Output }
    }

    $legacySeed = @'
INSERT INTO nodes (id, name, endpoint) VALUES ('node-guard', 'Guard Node', 'https://node-guard.test');
INSERT INTO instances (id, node_id, map_name, cluster_id) VALUES
    ('instance-legacy', 'node-guard', 'TheIsland_WP', 'cluster-legacy'),
    ('instance-guard', 'node-guard', 'TheIsland_WP', 'cluster-guard');
INSERT INTO port_allocations (node_id, protocol, port, instance_id) VALUES ('node-guard', 'udp', 7777, 'instance-legacy');
'@
    $result = Invoke-Psql $legacySeed
    if ($result.ExitCode -ne 0) { throw $result.Output }

    $result = Invoke-Psql $migration3[0]
    if ($result.ExitCode -ne 0) { throw $result.Output }

    $roleSeed = @'
INSERT INTO port_allocations (node_id, instance_id, port_role, protocol, host_port, container_port)
VALUES
    ('node-guard', 'instance-guard', 'game', 'udp', 7779, 7779),
    ('node-guard', 'instance-guard', 'peer', 'udp', 7778, 7778),
    ('node-guard', 'instance-guard', 'query', 'udp', 27015, 27015);
'@
    $result = Invoke-Psql $roleSeed
    if ($result.ExitCode -ne 0) { throw $result.Output }

    $downResult = Invoke-Psql $migration3[1]
    if ($downResult.ExitCode -eq 0) {
        throw 'Expected migration 003 down to fail for duplicate instance_id/protocol rows.'
    }
    if ($downResult.Output -notmatch 'multiple role rows share the same instance_id/protocol') {
        throw "Down migration failed without the expected safety message: $($downResult.Output)"
    }

    $postcondition = @'
DO $$
DECLARE
    allocation_count INTEGER;
    role_count INTEGER;
    has_role_column BOOLEAN;
    has_new_primary_key BOOLEAN;
    has_role_unique BOOLEAN;
    has_role_protocol_check BOOLEAN;
BEGIN
    SELECT COUNT(*) INTO allocation_count
      FROM port_allocations WHERE instance_id = 'instance-guard';
    SELECT COUNT(DISTINCT port_role) INTO role_count
      FROM port_allocations WHERE instance_id = 'instance-guard';
    SELECT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'port_allocations' AND column_name = 'port_role'
    ) INTO has_role_column;
    SELECT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'port_allocations'::regclass
          AND conname = 'port_allocations_pkey'
    ) INTO has_new_primary_key;
    SELECT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'port_allocations'::regclass
          AND conname = 'port_allocations_instance_id_port_role_key'
    ) INTO has_role_unique;
    SELECT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'port_allocations'::regclass
          AND conname = 'port_allocations_role_protocol_check'
    ) INTO has_role_protocol_check;
    IF allocation_count <> 3 OR role_count <> 3 OR NOT has_role_column
       OR NOT has_new_primary_key OR NOT has_role_unique OR NOT has_role_protocol_check THEN
        RAISE EXCEPTION 'failed downgrade changed role-aware allocations or schema';
    END IF;
END
$$;
'@
    $result = Invoke-Psql $postcondition
    if ($result.ExitCode -ne 0) { throw $result.Output }

    Write-Output 'port allocation down guard: PASS'
} finally {
    docker rm --force $container 2>$null | Out-Null
}
