#!/usr/bin/env bash
set -Eeuo pipefail
pgrep -f -- 'ArkAscendedServer.exe' >/dev/null
test -s /opt/ark/data/log/ready.marker
