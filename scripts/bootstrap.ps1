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

if ($envText -notmatch '(?m)^ADMIN_USERNAME=') {
    $envText = $envText.TrimEnd() + "`r`nADMIN_USERNAME=admin`r`n"
    Set-Content -Path $envPath -Value $envText -NoNewline
}

$secretDir = Join-Path $root '.secrets'
$adminSecretPath = Join-Path $secretDir 'admin_bootstrap_password'
New-Item -ItemType Directory -Path $secretDir -Force | Out-Null
if (-not (Test-Path $adminSecretPath) -or [string]::IsNullOrWhiteSpace((Get-Content $adminSecretPath -Raw))) {
    [IO.File]::WriteAllText($adminSecretPath, "$(New-PostgresPassword)`r`n", [Text.UTF8Encoding]::new($false))
    Write-Output 'Created the local admin bootstrap secret at .secrets\admin_bootstrap_password.'
}

# Keep local configuration and bootstrap credentials private without requiring
# SeSecurityPrivilege, which is commonly unavailable to standard Windows users.
foreach ($protectedPath in @($envPath, $adminSecretPath)) {
    $grant = "${env:USERNAME}:(F)"
    & icacls $protectedPath /inheritance:r /grant:r $grant | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "Could not protect $protectedPath with icacls." }
}

docker compose build control-plane agent web ark
if ($LASTEXITCODE -ne 0) { throw 'Docker image build failed.' }
docker compose up -d postgres control-plane socket-proxy agent web
if ($LASTEXITCODE -ne 0) { throw 'Docker Compose startup failed.' }
docker compose ps
if ($LASTEXITCODE -ne 0) { throw 'Could not inspect the Compose stack.' }

$health = Invoke-WebRequest 'http://127.0.0.1:8080/readyz' -UseBasicParsing
if ($health.StatusCode -ne 200) { throw "Control-plane readiness failed with HTTP $($health.StatusCode)." }
Write-Output 'bootstrap: PASS'
