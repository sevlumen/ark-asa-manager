# ARK ASA Platform

Self-hosted Docker platform for operating ARK: Survival Ascended servers.

## Runtime model

The supported deployment target is Ubuntu with Docker Engine and Compose v2. Docker
Desktop's Linux/WSL2 backend is suitable for local validation. WSL is an execution
environment only; it is not used to fake health checks or game-server readiness.

Services are defined in [`compose.yml`](compose.yml):

- `postgres`: PostgreSQL control-plane database
- `control-plane`: REST/WebSocket API
- `socket-proxy`: restricted Docker API used by the agent
- `agent`: node heartbeat and local Docker reconciliation endpoint
- `ark`: optional ARK runtime (`--profile ark`)
- `caddy` / `nginx`: selectable reverse-proxy profiles

## Quick start

```bash
cp .env.example .env
docker compose up -d postgres control-plane socket-proxy agent web
docker compose ps
curl http://localhost:8080/healthz
```

Open the operations console at <http://localhost:3000>. After signing in, the
admin can review the dashboard, servers, jobs, audit trail, nodes, and team
access pages. The API remains available at `http://localhost:8080` for local
diagnostics.

To start the ARK runtime, set Steam credentials and run:

```bash
docker compose --profile ark up -d ark
docker compose logs -f ark
```

The first start downloads the dedicated server into the persistent `ark-game`
volume. Save, config, logs, backups, and cluster data use separate volumes and
survive container recreation. The download and actual process readiness must pass
before the ARK service is considered healthy.

## Local development

```bash
make test
docker compose config
docker compose build control-plane agent ark
```

The repository contains multiple Go modules, so `go test ./...` from the
repository root is not the supported test command. `make test` runs the agent,
control-plane, CLI, and OpenAPI contract suites.

On Windows/PowerShell, use `powershell -ExecutionPolicy Bypass -File
scripts/test.ps1` for the same Go and web checks.

The CLI supports safe local operations:

```bash
arkctl backup
arkctl verify ark-save-<timestamp>.tar.gz
arkctl restore ark-save-<timestamp>.tar.gz  # requires the ARK instance stopped
```

The production checklist and recovery procedures are in [`docs/deployment.md`](docs/deployment.md).
