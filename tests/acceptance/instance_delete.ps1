$ErrorActionPreference = 'Stop'

$root = Resolve-Path (Join-Path $PSScriptRoot '..\..')
$base = if ($env:DELETE_PROBE_BASE_URL) { $env:DELETE_PROBE_BASE_URL.TrimEnd('/') } else { 'http://localhost:8080' }
$passwordFile = if ($env:ADMIN_BOOTSTRAP_PASSWORD_FILE) { $env:ADMIN_BOOTSTRAP_PASSWORD_FILE } else { Join-Path $root '.secrets\admin_bootstrap_password' }
$password = (Get-Content $passwordFile -Raw).Trim()
$session = New-Object Microsoft.PowerShell.Commands.WebRequestSession
$instanceID = 'delete-probe-' + [DateTimeOffset]::UtcNow.ToUnixTimeSeconds()
$containerName = "ark-$instanceID"
$volumeSuffixes = @('-game', '-save', '-config', '-logs', '-backups', '-cluster')

function Invoke-Api {
    param(
        [Parameter(Mandatory)][ValidateSet('GET', 'POST', 'DELETE')][string]$Method,
        [Parameter(Mandatory)][string]$Path,
        [hashtable]$Headers = @{},
        [object]$Body
    )
    $parameters = @{ Uri = "$base$Path"; Method = $Method; WebSession = $session; UseBasicParsing = $true; Headers = $Headers }
    if ($null -ne $Body) {
        $parameters.ContentType = 'application/json'
        $parameters.Body = ($Body | ConvertTo-Json -Compress)
    }
    try {
        return Invoke-WebRequest @parameters
    } catch {
        $response = $_.Exception.Response
        if ($null -eq $response) { throw }
        return [pscustomobject]@{ StatusCode = [int]$response.StatusCode; Content = '' }
    }
}

function Assert-Status([object]$Response, [int]$Expected, [string]$Message) {
    if ([int]$Response.StatusCode -ne $Expected) {
        throw "$Message (expected $Expected, got $($Response.StatusCode))"
    }
}

function Remove-ProbeVolumes {
    foreach ($suffixName in $volumeSuffixes) {
        try { & docker volume rm "ark-asa-platform_${instanceID}${suffixName}" 2>$null | Out-Null } catch {}
    }
}

$created = $false
try {
    $login = Invoke-Api POST '/api/v1/auth/login' -Body @{ username = 'admin'; password = $password }
    Assert-Status $login 200 'Admin login failed'
    $csrfCookie = $session.Cookies.GetCookies([Uri]$base) | Where-Object Name -eq 'ark_csrf' | Select-Object -First 1
    if ($null -eq $csrfCookie) { throw 'Login did not set CSRF cookie' }
    $writeHeaders = @{ 'X-CSRF-Token' = [Uri]::UnescapeDataString($csrfCookie.Value) }

    $create = Invoke-Api POST '/api/v1/instances' -Headers $writeHeaders -Body @{
        id = $instanceID; node_id = 'local-node'; map = 'TheIsland_WP'; cluster_id = 'delete-probe-cluster'; desired_state = 'stopped'
    }
    Assert-Status $create 201 'Probe instance creation failed'
    $created = $true

    $found = $false
    for ($attempt = 0; $attempt -lt 12; $attempt++) {
        if (docker ps -a --format '{{.Names}}' | Where-Object { $_ -eq $containerName }) { $found = $true; break }
        Start-Sleep -Seconds 2
    }
    if (-not $found) { throw 'Agent did not create the managed probe container' }

    $stopped = $false
    for ($attempt = 0; $attempt -lt 12; $attempt++) {
        $detail = Invoke-Api GET "/api/v1/instances/$instanceID"
        if ([int]$detail.StatusCode -eq 200 -and $detail.Content -and (($detail.Content | ConvertFrom-Json).observed_state -eq 'stopped')) { $stopped = $true; break }
        Start-Sleep -Seconds 2
    }
    if (-not $stopped) { throw 'Agent did not report the managed probe container as stopped' }

    $delete = Invoke-Api DELETE "/api/v1/instances/$instanceID" -Headers $writeHeaders
    Assert-Status $delete 204 'Instance deletion failed'

    $removed = $false
    for ($attempt = 0; $attempt -lt 12; $attempt++) {
        if (-not (docker ps -a --format '{{.Names}}' | Where-Object { $_ -eq $containerName })) { $removed = $true; break }
        Start-Sleep -Seconds 2
    }
    if (-not $removed) { throw 'Agent did not remove the deleted managed container' }
    Write-Output "instance delete acceptance: PASS ($instanceID)"
}
finally {
    if ($created) {
        try { $null = Invoke-Api DELETE "/api/v1/instances/$instanceID" -Headers $writeHeaders } catch {}
    }
    Remove-ProbeVolumes
    try { $null = Invoke-Api POST '/api/v1/auth/logout' -Headers $writeHeaders } catch {}
}
