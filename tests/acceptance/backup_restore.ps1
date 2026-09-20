$ErrorActionPreference = 'Stop'

$root = Resolve-Path (Join-Path $PSScriptRoot '..\..')
$base = if ($env:BACKUP_ACCEPTANCE_BASE_URL) { $env:BACKUP_ACCEPTANCE_BASE_URL.TrimEnd('/') } else { 'http://localhost:8080' }
$passwordFile = if ($env:ADMIN_BOOTSTRAP_PASSWORD_FILE) { $env:ADMIN_BOOTSTRAP_PASSWORD_FILE } else { Join-Path $root '.secrets\admin_bootstrap_password' }
$password = (Get-Content $passwordFile -Raw).Trim()
$session = New-Object Microsoft.PowerShell.Commands.WebRequestSession
$suffix = [DateTimeOffset]::UtcNow.ToUnixTimeSeconds()
$instanceID = "backup-probe-$suffix"
$sentinel = "recovery-sentinel-$suffix.txt"
$volumePrefix = 'ark-asa-platform'
$volumeNames = @(
    "$volumePrefix`_$instanceID-game", "$volumePrefix`_$instanceID-save", "$volumePrefix`_$instanceID-config",
    "$volumePrefix`_$instanceID-logs", "$volumePrefix`_$instanceID-backups", "$volumePrefix`_$instanceID-cluster"
)
$sessionHeaders = @{}
$instanceCreated = $false

function Invoke-Api {
    param(
        [Parameter(Mandatory)][ValidateSet('GET', 'POST', 'DELETE')][string]$Method,
        [Parameter(Mandatory)][string]$Path,
        [hashtable]$Headers = @{},
        [object]$Body
    )
    $parameters = @{ Uri = "$base$Path"; Method = $Method; WebSession = $session; UseBasicParsing = $true; Headers = $Headers }
    if ($null -ne $Body) { $parameters.ContentType = 'application/json'; $parameters.Body = ($Body | ConvertTo-Json -Compress) }
    try { return Invoke-WebRequest @parameters } catch {
        $response = $_.Exception.Response
        if ($null -eq $response) { throw }
        return [pscustomobject]@{ StatusCode = [int]$response.StatusCode; Content = '' }
    }
}

function Assert-Status([object]$Response, [int]$Expected, [string]$Message) {
    if ([int]$Response.StatusCode -ne $Expected) { throw "$Message (expected $Expected, got $($Response.StatusCode))" }
}

function Wait-Job([string]$id) {
    for ($attempt = 0; $attempt -lt 30; $attempt++) {
        $response = Invoke-Api GET "/api/v1/jobs/$id"
        Assert-Status $response 200 "Could not inspect job $id"
        $job = $response.Content | ConvertFrom-Json
        if ($job.status -in @('succeeded', 'failed')) { return $job }
        Start-Sleep -Seconds 2
    }
    throw "Job $id did not finish"
}

try {
    $login = Invoke-Api POST '/api/v1/auth/login' -Body @{ username = 'admin'; password = $password }
    Assert-Status $login 200 'Admin login failed'
    $csrfCookie = $session.Cookies.GetCookies([Uri]$base) | Where-Object Name -eq 'ark_csrf' | Select-Object -First 1
    if ($null -eq $csrfCookie) { throw 'Login did not set CSRF cookie' }
    $sessionHeaders = @{ 'X-CSRF-Token' = [Uri]::UnescapeDataString($csrfCookie.Value) }

    $create = Invoke-Api POST '/api/v1/instances' -Headers $sessionHeaders -Body @{ id = $instanceID; node_id = 'local-node'; map = 'TheIsland_WP'; cluster_id = "backup-cluster-$suffix"; desired_state = 'stopped' }
    Assert-Status $create 201 'Backup probe instance creation failed'
    $instanceCreated = $true
    $ready = $false
    for ($attempt = 0; $attempt -lt 15; $attempt++) {
        $detail = Invoke-Api GET "/api/v1/instances/$instanceID"
        if ([int]$detail.StatusCode -eq 200 -and (($detail.Content | ConvertFrom-Json).observed_state -eq 'stopped')) { $ready = $true; break }
        Start-Sleep -Seconds 2
    }
    if (-not $ready) { throw 'Backup probe instance did not become stopped' }

    docker run --rm --mount "type=volume,source=$volumePrefix`_$instanceID-save,target=/data" alpine:3.20 sh -c "printf 'backup-restore-proof' > /data/$sentinel" | Out-Null
    $backupRequest = Invoke-Api POST "/api/v1/instances/$instanceID/actions/backup" -Headers $sessionHeaders -Body @{}
    Assert-Status $backupRequest 202 'Backup action was not queued'
    $backupJob = Wait-Job (($backupRequest.Content | ConvertFrom-Json).id)
    if ($backupJob.status -ne 'succeeded') { throw "Backup job failed: $($backupJob.last_error)" }

    $backupsResponse = Invoke-Api GET '/api/v1/backups?limit=100'
    Assert-Status $backupsResponse 200 'Could not list backups'
    $backup = @($backupsResponse.Content | ConvertFrom-Json).items | Where-Object { $_.instance_id -eq $instanceID } | Select-Object -First 1
    if ($null -eq $backup) { throw 'Backup record was not created' }
    docker run --rm --mount "type=volume,source=$volumePrefix`_$instanceID-save,target=/data" alpine:3.20 sh -c "rm -f /data/$sentinel" | Out-Null

    $restoreRequest = Invoke-Api POST "/api/v1/instances/$instanceID/actions/restore" -Headers $sessionHeaders -Body @{ backup_id = $backup.id }
    Assert-Status $restoreRequest 202 'Restore action was not queued'
    $restoreJob = Wait-Job (($restoreRequest.Content | ConvertFrom-Json).id)
    if ($restoreJob.status -ne 'succeeded') { throw "Restore job failed: $($restoreJob.last_error)" }
    docker run --rm --mount "type=volume,source=$volumePrefix`_$instanceID-save,target=/data" alpine:3.20 sh -c "test -f /data/$sentinel" | Out-Null
    Write-Output "backup restore acceptance: PASS ($instanceID)"
}
finally {
    if ($instanceCreated) {
        try { $null = Invoke-Api DELETE "/api/v1/instances/$instanceID" -Headers $sessionHeaders } catch {}
        Start-Sleep -Seconds 12
    }
    foreach ($volume in $volumeNames) { docker volume rm $volume 2>$null | Out-Null }
    try { $null = Invoke-Api POST '/api/v1/auth/logout' -Headers $sessionHeaders } catch {}
}
