$ErrorActionPreference = 'Stop'

$root = Resolve-Path (Join-Path $PSScriptRoot '..')
Set-Location $root

if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
    throw 'docker is required.'
}
docker compose version | Out-Null
if ($LASTEXITCODE -ne 0) {
    throw 'Docker Compose plugin is required.'
}

function New-PostgresPassword {
    $bytes = New-Object byte[] 24
    $random = [Security.Cryptography.RandomNumberGenerator]::Create()
    try { $random.GetBytes($bytes) } finally { $random.Dispose() }
    return ([BitConverter]::ToString($bytes).Replace('-', '')).ToLowerInvariant()
}

$envPath = Join-Path $root '.env'
if (-not (Test-Path $envPath)) {
    Copy-Item (Join-Path $root '.env.example') $envPath
}
$envText = Get-Content $envPath -Raw
if ($envText -match '(?m)^POSTGRES_PASSWORD=(?:$|change-me$)') {
    $envText = [regex]::Replace($envText, '(?m)^POSTGRES_PASSWORD=.*$', "POSTGRES_PASSWORD=$(New-PostgresPassword)")
    Set-Content -Path $envPath -Value $envText -NoNewline
    Write-Output 'Generated a PostgreSQL password in .env.'
}

# Keep the local environment file private on Windows as well as Unix hosts.
$acl = Get-Acl $envPath
$acl.SetAccessRuleProtection($true, $false)
$rule = New-Object System.Security.AccessControl.FileSystemAccessRule($env:USERNAME, 'FullControl', 'Allow')
$acl.SetAccessRule($rule)
Set-Acl -Path $envPath -AclObject $acl

docker compose build control-plane agent web
if ($LASTEXITCODE -ne 0) { throw 'Docker image build failed.' }
docker compose up -d postgres control-plane socket-proxy agent web
if ($LASTEXITCODE -ne 0) { throw 'Docker Compose startup failed.' }
docker compose ps
if ($LASTEXITCODE -ne 0) { throw 'Could not inspect the Compose stack.' }

$health = Invoke-WebRequest 'http://127.0.0.1:8080/readyz' -UseBasicParsing
if ($health.StatusCode -ne 200) { throw "Control-plane readiness failed with HTTP $($health.StatusCode)." }
Write-Output 'bootstrap: PASS'
