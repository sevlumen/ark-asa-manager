$ErrorActionPreference = 'Stop'

$root = Resolve-Path (Join-Path $PSScriptRoot '..\..')
$base = if ($env:NODE_ENROLLMENT_BASE_URL) { $env:NODE_ENROLLMENT_BASE_URL.TrimEnd('/') } else { 'http://localhost:8080' }
$passwordFile = if ($env:ADMIN_BOOTSTRAP_PASSWORD_FILE) { $env:ADMIN_BOOTSTRAP_PASSWORD_FILE } else { Join-Path $root '.secrets\admin_bootstrap_password' }
$password = (Get-Content $passwordFile -Raw).Trim()
$session = New-Object Microsoft.PowerShell.Commands.WebRequestSession
$suffix = [DateTimeOffset]::UtcNow.ToUnixTimeSeconds()
$nodeName = "remote-enrollment-probe-$suffix"
$stateVolume = "ark-asa-platform_enrollment-probe-$suffix"
$agentName = "ark-enrollment-probe-$suffix"
$tokenFile = [IO.Path]::GetTempFileName()
$nodeID = ''
$writeHeaders = @{}

function Invoke-Api {
    param(
        [Parameter(Mandatory)][ValidateSet('GET', 'POST')][string]$Method,
        [Parameter(Mandatory)][string]$Path,
        [hashtable]$Headers = @{},
        [object]$Body
    )
    $parameters = @{ Uri = "$base$Path"; Method = $Method; WebSession = $session; UseBasicParsing = $true; Headers = $Headers }
    if ($null -ne $Body) {
        $parameters.ContentType = 'application/json'
        $parameters.Body = ($Body | ConvertTo-Json -Compress)
    }
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
    $csrfCookie = $session.Cookies.GetCookies([Uri]$base) | Where-Object Name -eq 'ark_csrf' | Select-Object -First 1
    if ($null -eq $csrfCookie) { throw 'Login did not set CSRF cookie' }
    $writeHeaders = @{ 'X-CSRF-Token' = [Uri]::UnescapeDataString($csrfCookie.Value) }

    $created = Invoke-Api POST '/api/v1/nodes' -Headers $writeHeaders -Body @{ name = $nodeName; endpoint = 'https://remote-enrollment.invalid' }
    Assert-Status $created 201 'Node registration failed'
    $registration = $created.Content | ConvertFrom-Json
    $nodeID = [string]$registration.id
    if ([string]::IsNullOrWhiteSpace($nodeID) -or [string]::IsNullOrWhiteSpace($registration.enrollment_token)) { throw 'Node registration did not return enrollment material' }
    Set-Content -LiteralPath $tokenFile -Value ([string]$registration.enrollment_token) -NoNewline

    $null = docker volume create $stateVolume
    $helper = docker create --name "$agentName-token-helper" --mount "type=volume,source=$stateVolume,target=/state" alpine:3.20 sh -c 'sleep 300'
    docker start $helper | Out-Null
    docker cp $tokenFile "$helper`:/state/token"
    docker exec $helper chown 65532:65532 /state /state/token | Out-Null
    docker exec $helper chmod 600 /state/token | Out-Null
    docker rm -f $helper | Out-Null

    $runArgs = @(
        'run', '-d', '--name', $agentName, '--network', 'ark-asa-platform_default', '--user', '65532:65532',
        '--mount', "type=volume,source=$stateVolume,target=/state",
        '--mount', 'type=volume,source=ark-asa-platform_control-plane-tls,target=/run/ark-tls,readonly',
        '-e', 'CONTROL_PLANE_URL=http://control-plane:8080',
        '-e', 'CONTROL_PLANE_INTERNAL_URL=https://control-plane:8443',
        '-e', "NODE_ID=$nodeID",
        '-e', 'DOCKER_HOST=tcp://socket-proxy:2375',
        '-e', 'AGENT_TLS_CERT_FILE=/state/agent.pem',
        '-e', 'AGENT_TLS_KEY_FILE=/state/agent-key.pem',
        '-e', 'AGENT_TLS_CA_FILE=/run/ark-tls/ca.pem',
        '-e', 'AGENT_ENROLLMENT_CA_FILE=/run/ark-tls/enrollment-ca.pem',
        '-e', 'NODE_ENROLLMENT_TOKEN_FILE=/state/token',
        '-e', 'ARK_RUNTIME_IMAGE=ark-asa-runtime:local',
        '-e', 'ARK_VOLUME_PREFIX=ark-asa-platform',
        '-e', 'ARK_MEMORY_LIMIT=512m',
        '-e', 'ARK_CPUS=1',
        'ark-asa-agent:local'
    )
    docker @runArgs | Out-Null

    $verified = $false
    for ($attempt = 0; $attempt -lt 20; $attempt++) {
        $status = docker inspect $agentName --format '{{.State.Status}}' 2>$null
        $row = docker exec ark-asa-platform-postgres-1 psql -U ark -d ark -Atc "select status || '|' || case when exists (select 1 from node_certificates where node_id='$nodeID') then 'certified' else 'uncertified' end from nodes where id='$nodeID'" 2>$null
        if ($status -eq 'running' -and $row -match '^online\|certified$') { $verified = $true; break }
        Start-Sleep -Seconds 2
    }
    if (-not $verified) {
        docker logs --tail 20 $agentName 2>&1
        throw 'Enrolled probe agent did not become online with a recorded certificate'
    }
    Write-Output "node enrollment acceptance: PASS ($nodeName)"
}
finally {
    docker rm -f $agentName 2>$null | Out-Null
    if ($nodeID) { docker exec ark-asa-platform-postgres-1 psql -U ark -d ark -c "delete from nodes where id='$nodeID'" 2>$null | Out-Null }
    docker volume rm $stateVolume 2>$null | Out-Null
    Remove-Item -LiteralPath $tokenFile -Force -ErrorAction SilentlyContinue
    try { $null = Invoke-Api POST '/api/v1/auth/logout' -Headers $writeHeaders } catch {}
}
