$ErrorActionPreference = 'Stop'

$root = Resolve-Path (Join-Path $PSScriptRoot '..\..')
$nginx = Get-Content (Join-Path $root 'deploy\nginx\default.conf') -Raw

foreach ($required in @(
    'location /api/v1/ws',
    'proxy_http_version 1.1',
    'proxy_set_header Upgrade $http_upgrade',
    'proxy_set_header Connection "upgrade"'
)) {
    if ($nginx -notmatch [regex]::Escape($required)) {
        throw "Nginx WebSocket proxy contract is missing: $required"
    }
}

Write-Output 'proxy contract: PASS (nginx websocket upgrade)'
