$ErrorActionPreference = 'Stop'

$root = Resolve-Path (Join-Path $PSScriptRoot '..\..')
$base = if ($env:RBAC_BASE_URL) { $env:RBAC_BASE_URL.TrimEnd('/') } else { 'http://localhost:8080' }
$passwordFile = if ($env:ADMIN_BOOTSTRAP_PASSWORD_FILE) { $env:ADMIN_BOOTSTRAP_PASSWORD_FILE } else { Join-Path $root '.secrets\admin_bootstrap_password' }
$adminPassword = (Get-Content $passwordFile -Raw).Trim()
$suffix = [DateTimeOffset]::UtcNow.ToUnixTimeSeconds()
$viewerName = "rbac-viewer-$suffix"
$operatorName = "rbac-operator-$suffix"
$viewerPassword = "Viewer-pass-$suffix-strong"
$operatorPassword = "Operator-pass-$suffix-strong"
$instanceID = "rbac-probe-$suffix"
$containerName = "ark-$instanceID"
$volumeSuffixes = @('-game', '-save', '-config', '-logs', '-backups', '-cluster')
$session = New-Object Microsoft.PowerShell.Commands.WebRequestSession
$createdUsers = @()
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

function Remove-ProbeContainer {
    try { & docker rm -f $containerName 2>$null | Out-Null } catch {}
}

function Remove-ProbeVolumes {
    foreach ($suffixName in $volumeSuffixes) {
        try { & docker volume rm "ark-asa-platform_${instanceID}${suffixName}" 2>$null | Out-Null } catch {}
    }
}

function Login([string]$username, [string]$password) {
    $login = Invoke-Api POST '/api/v1/auth/login' -Body @{ username = $username; password = $password }
    Assert-Status $login 200 "Login failed for $username"
    $cookie = $session.Cookies.GetCookies([Uri]$base) | Where-Object Name -eq 'ark_csrf' | Select-Object -First 1
    if ($null -eq $cookie) { throw "Login did not set CSRF for $username" }
    return @{ 'X-CSRF-Token' = [Uri]::UnescapeDataString($cookie.Value) }
}

try {
    $adminHeaders = Login 'admin' $adminPassword
    foreach ($user in @(
        @{ username = $viewerName; password = $viewerPassword; role = 'viewer' },
        @{ username = $operatorName; password = $operatorPassword; role = 'operator' }
    )) {
        $created = Invoke-Api POST '/api/v1/users' -Headers $adminHeaders -Body $user
        Assert-Status $created 201 "Could not create $($user.role) probe user"
        $createdUsers += $user.username
    }
    $null = Invoke-Api POST '/api/v1/auth/logout' -Headers $adminHeaders

    $viewerHeaders = Login $viewerName $viewerPassword
    $read = Invoke-Api GET '/api/v1/instances?limit=5'
    Assert-Status $read 200 'Viewer read access failed'
    $denied = Invoke-Api POST '/api/v1/instances/theisland/actions/restart' -Headers $viewerHeaders -Body @{}
    Assert-Status $denied 403 'Viewer mutation was not rejected'
    $null = Invoke-Api POST '/api/v1/auth/logout' -Headers $viewerHeaders

    $operatorHeaders = Login $operatorName $operatorPassword
    $create = Invoke-Api POST '/api/v1/instances' -Headers $operatorHeaders -Body @{ id = $instanceID; node_id = 'local-node'; map = 'TheIsland_WP'; cluster_id = "rbac-cluster-$suffix"; desired_state = 'stopped' }
    Assert-Status $create 201 'Operator instance mutation failed'
    $instanceCreated = $true
    $stopped = $false
    for ($attempt = 0; $attempt -lt 15; $attempt++) {
        $detail = Invoke-Api GET "/api/v1/instances/$instanceID"
        if ([int]$detail.StatusCode -eq 200 -and (($detail.Content | ConvertFrom-Json).observed_state -eq 'stopped')) { $stopped = $true; break }
        Start-Sleep -Seconds 2
    }
    if (-not $stopped) { throw 'Operator probe instance did not become stopped' }
    $delete = Invoke-Api DELETE "/api/v1/instances/$instanceID" -Headers $operatorHeaders
    Assert-Status $delete 204 'Operator instance delete failed'
    $instanceCreated = $false
    $null = Invoke-Api POST '/api/v1/auth/logout' -Headers $operatorHeaders

    $adminHeaders = Login 'admin' $adminPassword
    $auditResponse = Invoke-Api GET '/api/v1/audit?limit=100'
    Assert-Status $auditResponse 200 'Could not read audit records'
    $audit = $auditResponse.Content | ConvertFrom-Json
    $deniedAudit = @($audit.items | Where-Object { $_.actor -eq $viewerName -and $_.action -eq 'instance.action' -and $_.metadata.outcome -eq 'denied' })
    if ($deniedAudit.Count -lt 1) { throw 'Viewer denied mutation was not recorded in audit trail' }
    Write-Output "rbac acceptance: PASS ($viewerName, $operatorName)"
}
finally {
    if ($instanceCreated) { docker exec ark-asa-platform-postgres-1 psql -U ark -d ark -c "delete from instances where id='$instanceID'" 2>$null | Out-Null }
    Remove-ProbeContainer
    Remove-ProbeVolumes
    foreach ($username in $createdUsers) { docker exec ark-asa-platform-postgres-1 psql -U ark -d ark -c "delete from users where username='$username'" 2>$null | Out-Null }
    try { $null = Invoke-Api POST '/api/v1/auth/logout' -Headers $adminHeaders } catch {}
}
