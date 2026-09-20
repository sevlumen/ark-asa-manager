$ErrorActionPreference = 'Stop'

$root = Resolve-Path (Join-Path $PSScriptRoot '..\..')
$base = if ($env:WEB_SMOKE_BASE_URL) { $env:WEB_SMOKE_BASE_URL.TrimEnd('/') } else { 'http://localhost:3000' }
$username = if ($env:ADMIN_USERNAME) { $env:ADMIN_USERNAME } else { 'admin' }
$passwordFile = if ($env:ADMIN_BOOTSTRAP_PASSWORD_FILE) { $env:ADMIN_BOOTSTRAP_PASSWORD_FILE } else { Join-Path $root '.secrets\admin_bootstrap_password' }

if (-not (Test-Path $passwordFile)) {
    throw "Admin bootstrap password file is missing: $passwordFile"
}
$password = (Get-Content $passwordFile -Raw).Trim()
if (-not $password) {
    throw 'Admin bootstrap password file is empty.'
}

$session = New-Object Microsoft.PowerShell.Commands.WebRequestSession
$jsonHeaders = @{ Accept = 'application/json' }

function Invoke-SmokeRequest {
    param(
        [Parameter(Mandatory)][ValidateSet('GET', 'POST')][string]$Method,
        [Parameter(Mandatory)][string]$Path,
        [hashtable]$Headers = @{},
        [object]$Body
    )
    $requestHeaders = @{} + $jsonHeaders + $Headers
    $parameters = @{
        Uri = "$base$Path"
        Method = $Method
        WebSession = $session
        Headers = $requestHeaders
        UseBasicParsing = $true
    }
    if ($null -ne $Body) {
        $parameters.ContentType = 'application/json'
        $parameters.Body = ($Body | ConvertTo-Json -Compress)
    }
    try {
        $response = Invoke-WebRequest @parameters
        return [pscustomobject]@{ StatusCode = [int]$response.StatusCode; Body = $response.Content }
    } catch {
        $webError = $_
        $response = $webError.Exception.Response
        if ($null -eq $response) { throw }
        if ($response -is [System.Net.Http.HttpResponseMessage]) {
            try { $content = $response.Content.ReadAsStringAsync().GetAwaiter().GetResult() } catch { $content = $webError.ErrorDetails.Message }
        } else {
            try {
                $reader = New-Object IO.StreamReader($response.GetResponseStream())
                try { $content = $reader.ReadToEnd() } finally { $reader.Dispose() }
            } catch { $content = $webError.ErrorDetails.Message }
        }
        if ($null -eq $content) { $content = '' }
        return [pscustomobject]@{ StatusCode = [int]$response.StatusCode; Body = $content }
    }
}

function Assert-Status([object]$Response, [int]$Expected, [string]$Message) {
    if ($Response.StatusCode -ne $Expected) {
        throw "$Message (expected $Expected, got $($Response.StatusCode))"
    }
}

$health = Invoke-SmokeRequest GET '/healthz'
Assert-Status $health 200 'Public health endpoint failed'

$login = Invoke-SmokeRequest POST '/api/v1/auth/login' -Body @{ username = $username; password = $password }
Assert-Status $login 200 'Admin login failed'

$csrfCookie = $session.Cookies.GetCookies([Uri]$base) | Where-Object Name -eq 'ark_csrf' | Select-Object -First 1
if ($null -eq $csrfCookie -or [string]::IsNullOrWhiteSpace($csrfCookie.Value)) {
    throw 'Login did not set a CSRF cookie.'
}
$csrf = [Uri]::UnescapeDataString($csrfCookie.Value)
$writeHeaders = @{ 'X-CSRF-Token' = $csrf }

foreach ($path in @('/api/v1/me', '/api/v1/system/health', '/api/v1/instances?limit=50', '/api/v1/jobs?limit=50', '/api/v1/audit?limit=50', '/api/v1/nodes?limit=50', '/api/v1/users')) {
    $response = Invoke-SmokeRequest GET $path
    Assert-Status $response 200 "Admin API request failed: $path"
}

$csrfDenied = Invoke-SmokeRequest POST '/api/v1/instances/theisland/actions/restart' -Body @{}
Assert-Status $csrfDenied 403 'Mutation without CSRF was not rejected'

$logout = Invoke-SmokeRequest POST '/api/v1/auth/logout' -Headers $writeHeaders
Assert-Status $logout 200 'Admin logout failed'

Write-Output "web smoke: PASS ($base)"
