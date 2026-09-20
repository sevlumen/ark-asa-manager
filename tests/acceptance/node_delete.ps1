$ErrorActionPreference = 'Stop'

$root = Resolve-Path (Join-Path $PSScriptRoot '..\..')
$base = if ($env:NODE_DELETE_BASE_URL) { $env:NODE_DELETE_BASE_URL.TrimEnd('/') } else { 'http://localhost:8080' }
$passwordFile = if ($env:ADMIN_BOOTSTRAP_PASSWORD_FILE) { $env:ADMIN_BOOTSTRAP_PASSWORD_FILE } else { Join-Path $root '.secrets\admin_bootstrap_password' }
$password = (Get-Content $passwordFile -Raw).Trim()
$session = New-Object Microsoft.PowerShell.Commands.WebRequestSession
$name = "node-delete-probe-" + [DateTimeOffset]::UtcNow.ToUnixTimeSeconds()
$nodeID = ''
$headers = @{}

function Invoke-Api {
    param([Parameter(Mandatory)][ValidateSet('GET', 'POST', 'DELETE')][string]$Method, [Parameter(Mandatory)][string]$Path, [hashtable]$Headers = @{}, [object]$Body)
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

try {
    $login = Invoke-Api POST '/api/v1/auth/login' -Body @{ username = 'admin'; password = $password }
    Assert-Status $login 200 'Admin login failed'
    $cookie = $session.Cookies.GetCookies([Uri]$base) | Where-Object Name -eq 'ark_csrf' | Select-Object -First 1
    if ($null -eq $cookie) { throw 'Login did not set CSRF cookie' }
    $headers = @{ 'X-CSRF-Token' = [Uri]::UnescapeDataString($cookie.Value) }
    $created = Invoke-Api POST '/api/v1/nodes' -Headers $headers -Body @{ name = $name; endpoint = 'https://node-delete.invalid' }
    Assert-Status $created 201 'Node creation failed'
    $nodeID = [string](($created.Content | ConvertFrom-Json).id)
    $deleted = Invoke-Api DELETE "/api/v1/nodes/$nodeID" -Headers $headers
    Assert-Status $deleted 204 'Offline node deletion failed'
    $remaining = docker exec ark-asa-platform-postgres-1 psql -U ark -d ark -Atc "select count(*) from nodes where id='$nodeID'" 2>$null
    if ([int]$remaining.Trim() -ne 0) { throw 'Deleted node still exists in PostgreSQL' }
    Write-Output "node delete acceptance: PASS ($name)"
}
finally {
    if ($nodeID) { docker exec ark-asa-platform-postgres-1 psql -U ark -d ark -c "delete from nodes where id='$nodeID'" 2>$null | Out-Null }
    try { $null = Invoke-Api POST '/api/v1/auth/logout' -Headers $headers } catch {}
}
