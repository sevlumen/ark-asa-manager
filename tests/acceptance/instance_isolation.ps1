$ErrorActionPreference = 'Stop'

$root = Resolve-Path (Join-Path $PSScriptRoot '..\..')
$base = if ($env:ISOLATION_PROBE_BASE_URL) { $env:ISOLATION_PROBE_BASE_URL.TrimEnd('/') } else { 'http://localhost:8080' }
$passwordFile = if ($env:ADMIN_BOOTSTRAP_PASSWORD_FILE) { $env:ADMIN_BOOTSTRAP_PASSWORD_FILE } else { Join-Path $root '.secrets\admin_bootstrap_password' }
$password = (Get-Content $passwordFile -Raw).Trim()
$session = New-Object Microsoft.PowerShell.Commands.WebRequestSession
$suffix = [DateTimeOffset]::UtcNow.ToUnixTimeSeconds()
$ids = @("isolation-a-$suffix", "isolation-b-$suffix")
$created = New-Object System.Collections.Generic.List[string]

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
    if ([int]$Response.StatusCode -ne $Expected) { throw "$Message (expected $Expected, got $($Response.StatusCode))" }
}

function Get-Instance([string]$id) {
    $response = Invoke-Api GET "/api/v1/instances/$id"
    Assert-Status $response 200 "Could not read instance $id"
    return $response.Content | ConvertFrom-Json
}

function Get-Container([string]$name) {
    try {
        $raw = & docker inspect $name 2>$null
        if ($LASTEXITCODE -ne 0 -or -not $raw) { return $null }
        return ($raw -join "`n") | ConvertFrom-Json | Select-Object -First 1
    } catch {
        return $null
    }
}

$writeHeaders = @{}
try {
    $login = Invoke-Api POST '/api/v1/auth/login' -Body @{ username = 'admin'; password = $password }
    Assert-Status $login 200 'Admin login failed'
    $csrfCookie = $session.Cookies.GetCookies([Uri]$base) | Where-Object Name -eq 'ark_csrf' | Select-Object -First 1
    if ($null -eq $csrfCookie) { throw 'Login did not set CSRF cookie' }
    $writeHeaders = @{ 'X-CSRF-Token' = [Uri]::UnescapeDataString($csrfCookie.Value) }

    foreach ($id in $ids) {
        $response = Invoke-Api POST '/api/v1/instances' -Headers $writeHeaders -Body @{
            id = $id; node_id = 'local-node'; map = 'TheIsland_WP'; cluster_id = "isolation-cluster-$suffix"; desired_state = 'stopped'
        }
        Assert-Status $response 201 "Could not create $id"
        $created.Add($id)
    }

    $details = @{}
    $containers = @{}
    foreach ($id in $ids) {
        $name = "ark-$id"
        $ready = $false
        for ($attempt = 0; $attempt -lt 15; $attempt++) {
            $detail = Get-Instance $id
            $container = Get-Container $name
            if ($detail.observed_state -eq 'stopped' -and $null -ne $container) { $ready = $true; break }
            Start-Sleep -Seconds 2
        }
        if (-not $ready) { throw "Instance $id did not reach observed stopped with a managed container" }
        $details[$id] = $detail
        $containers[$id] = $container
    }

    $gamePorts = @($details[$ids[0]].ports.game, $details[$ids[1]].ports.game)
    $queryPorts = @($details[$ids[0]].ports.query, $details[$ids[1]].ports.query)
    if ($gamePorts[0] -eq $gamePorts[1] -or $queryPorts[0] -eq $queryPorts[1]) { throw 'Allocated game/query ports are not isolated' }

    foreach ($id in $ids) {
        $container = $containers[$id]
        if ($container.Config.Labels.'ark.platform.instance-id' -ne $id) { throw "Instance label missing on $id" }
        if ($container.Config.Labels.'ark.platform.node-id' -ne 'local-node') { throw "Node label missing on $id" }
        $mountNames = @($container.Mounts | ForEach-Object { $_.Name })
        foreach ($suffixName in @('-game', '-save', '-config', '-logs', '-backups', '-cluster')) {
            if (-not ($mountNames -contains "ark-asa-platform_$id$suffixName")) { throw "Persistence volume $suffixName missing on $id" }
        }
        $bindings = $container.HostConfig.PortBindings.PSObject.Properties.Name
        if (-not ($bindings -contains "$($details[$id].ports.game)/udp") -or -not ($bindings -contains "$($details[$id].ports.query)/udp")) { throw "Game/query bindings missing on $id" }
    }

    foreach ($id in $ids) {
        $delete = Invoke-Api DELETE "/api/v1/instances/$id" -Headers $writeHeaders
        Assert-Status $delete 204 "Could not delete $id"
    }
    foreach ($id in $ids) {
        $name = "ark-$id"
        for ($attempt = 0; $attempt -lt 15; $attempt++) {
            if ($null -eq (Get-Container $name)) { break }
            Start-Sleep -Seconds 2
        }
        if ($null -ne (Get-Container $name)) { throw "Managed container $name was not removed" }
    }
    Write-Output "instance isolation acceptance: PASS ($($ids -join ', '))"
}
finally {
    foreach ($id in $created) {
        try { $null = Invoke-Api DELETE "/api/v1/instances/$id" -Headers $writeHeaders } catch {}
    }
    try { $null = Invoke-Api POST '/api/v1/auth/logout' -Headers $writeHeaders } catch {}
}
