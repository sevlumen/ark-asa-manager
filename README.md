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
go test ./...
docker compose config
docker compose build control-plane agent ark
```

The production checklist and recovery procedures are in [`docs/deployment.md`](docs/deployment.md).
