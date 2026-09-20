# ARK ASA Platform Production Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete the Docker-first ARK ASA platform and prove the pasted Definition of Done with real containers, persistent data, and acceptance tests.

**Architecture:** A Go/PostgreSQL control-plane owns durable desired state and jobs. Outbound mTLS agents reconcile instance specifications into non-root Docker containers through a restricted socket proxy; the Bun/React UI and `arkctl` use the same OpenAPI contract.

**Tech Stack:** Docker Compose, Ubuntu 24.04, SteamCMD, pinned GE-Proton, ASA dedicated server, Go, chi, pgx, sqlc, goose, PostgreSQL, WebSocket, Bun, React, TypeScript, Tailwind, Caddy, Nginx, NFS, S3-compatible object storage.

**Spec:** `docs/superpowers/specs/2026-09-13-ark-asa-platform-production-design.md` and the pasted requirements file `C:\Users\quang\.codex\attachments\b4b91d52-4f73-47b2-8239-205b70127866\pasted-text-1.txt`.

## Global Constraints

- “Docker/Compose is the execution boundary.”
- “PostgreSQL is the only control-plane database. SQLite is not a fallback.”
- “A mock, fake health response, or simulated game process cannot satisfy an acceptance criterion.”
- “Game files, save, config, logs, backups, and cluster data are separate persistence boundaries.”
- “The control-plane never receives the host Docker socket.”
- Default map is `TheIsland_WP`; default player limit is `10`; PvE and crossplay are enabled.
- Every production behavior change gets a failing test or reproducible smoke check before implementation and fresh verification after implementation.

---

### Task 1: Contract, migrations, and generated database layer

**Files:**
- Create: `api/openapi/openapi.yaml`
- Create: `api/openapi/README.md`
- Create: `db/migrations/002_platform.sql`
- Create: `deploy/docker/migrate/Dockerfile`
- Create: `db/goose.yaml`
- Create: `db/queries/nodes.sql`
- Create: `db/queries/jobs.sql`
- Create: `db/queries/users.sql`
- Create: `db/queries/audit.sql`
- Modify: `db/sqlc.yaml`
- Create: `tests/contracts/openapi_test.go`

**Interfaces:**
- `POST /api/v1/auth/login`, `POST /api/v1/auth/logout`, `GET /api/v1/me`.
- `GET/POST/PATCH /api/v1/nodes`.
- `GET/POST/PATCH/DELETE /api/v1/instances`.
- `POST /api/v1/instances/{id}/actions/{action}` where action is one of
  `start`, `stop`, `restart`, `update`, `backup`, `restore`.
- `GET /api/v1/jobs/{id}`, `GET /api/v1/audit`, and WebSocket
  `/api/v1/ws?cursor=<event-id>`.
- SQL query functions return typed rows for nodes, instances, jobs, users,
  roles, sessions, API tokens, encrypted secrets, and audit entries.

- [x] **Step 1: Write the contract test** that loads `openapi.yaml`, asserts every listed path exists, and rejects an operation without a response schema.
- [x] **Step 2: Run the contract test** in a Go container and verify it fails because the OpenAPI file is absent/incomplete.
- [x] **Step 3: Add the OpenAPI paths and schemas** for principals, nodes, instances, jobs, backups, audit records, and event envelopes.
- [x] **Step 4: Add idempotent PostgreSQL migration 002** with users, roles, sessions, tokens, node certificates, instance status, jobs, job attempts, backup records, event cursor, encrypted secrets, and audit indexes.
- [x] **Step 5: Add goose configuration and sqlc query files** using pgx/v5 and PostgreSQL types; never add SQLite code.
- [x] **Step 6: Run the contract test and `sqlc generate` in Docker**; require exit code 0 and a clean generated package.

### Task 2: Real ARK runtime image and persistence contract

**Files:**
- Modify: `deploy/docker/ark-runtime/Dockerfile`
- Modify: `deploy/docker/ark-runtime/entrypoint.sh`
- Modify: `deploy/docker/ark-runtime/healthcheck.sh`
- Create: `deploy/docker/ark-runtime/download-runtime.sh`
- Create: `deploy/docker/ark-runtime/runtime.env.example`
- Modify: `compose.yml`
- Create: `tests/runtime/runtime_contract.ps1`

**Interfaces:**
- Image build accepts `PROTON_VERSION`, `PROTON_SHA256`, and
  `STEAMCMD_SHA256`; download failures are non-zero and retry up to three
  times with visible progress.
- Entrypoint consumes `ARK_MAP`, `ARK_PORT`, `ARK_QUERY_PORT`, `ARK_RCON_PORT`,
  `ARK_MAX_PLAYERS`, `ARK_CLUSTER_ID`, `ARK_SERVER_NAME`, `ARK_PVE`,
  `ARK_CROSSPLAY`, `ARK_BATTLEYE`, `ARK_UPDATE_ON_START`, `STEAM_USER`, and
  `STEAM_PASSWORD`.
- Healthcheck returns success only when the real `ArkAscendedServer.exe`
  process is alive and its readiness marker is present in the log.

- [ ] **Step 1: Write runtime contract tests** for non-root UID, separate mounts, default map/player settings, and failure when the server executable is absent.
- [ ] **Step 2: Run the contract tests** against the current image and capture the expected failures.
- [ ] **Step 3: Implement checksum-verified download** with `curl --retry`, progress output, and a cached BuildKit layer; keep game installation at first start in `ark-game`.
- [ ] **Step 4: Implement explicit save/config/log symlinks** and a readiness marker written only after the real server process emits its loaded-map line.
- [ ] **Step 5: Add Compose resource limits, JSON log rotation, separate volumes, and configurable ports/cluster paths.**
- [ ] **Step 6: Build the image with plain progress and run the runtime contract tests.** A successful image build alone is insufficient; start the container with real ASA files and inspect health/log output.

### Task 3: Docker-native operations and `arkctl`

**Files:**
- Create: `cmd/arkctl/internal/compose/runner.go`
- Create: `cmd/arkctl/internal/backup/backup.go`
- Create: `cmd/arkctl/internal/instance/instance.go`
- Modify: `cmd/arkctl/main.go`
- Create: `cmd/arkctl/Dockerfile`
- Create: `deploy/docker/arkctl-compose.yml`
- Create: `tests/operations/arkctl_test.go`
- Modify: `scripts/install-arkctl.sh`

**Interfaces:**
- `arkctl instance create|start|stop|restart|status|logs|update`.
- `arkctl backup create|verify|restore` with archive manifest, SHA-256, source
  volume, destination volume, and restore lock.
- `arkctl node status` and `arkctl doctor`.
- Every Docker call is executed in the CLI image through the socket proxy or
  an explicitly scoped Docker API endpoint; restore rejects path traversal and
  refuses to overwrite a running instance.

- [ ] **Step 1: Write tests** for command-to-Compose argument mapping, archive manifest verification, unsafe restore names, and refusal to restore while running.
- [ ] **Step 2: Run tests** and verify they fail against the current thin CLI.
- [ ] **Step 3: Implement the typed operation runner** with context timeouts and captured stdout/stderr.
- [ ] **Step 4: Implement backup create/verify/restore** using a temporary Docker helper container and an atomic restore directory.
- [ ] **Step 5: Package `arkctl` as a Docker image** and update Linux/PowerShell wrappers to call `docker compose run --rm`.
- [ ] **Step 6: Exercise all commands against a disposable real instance** and verify a sentinel file survives recreate/update/restore.

### Task 4: Instance model, port allocation, and agent reconciliation

**Files:**
- Create: `apps/agent/internal/reconcile/reconciler.go`
- Create: `apps/agent/internal/docker/client.go`
- Create: `apps/agent/internal/checkpoint/checkpoint.go`
- Modify: `apps/agent/main.go`
- Create: `db/queries/port_allocations.sql`
- Create: `tests/agent/reconcile_test.go`
- Modify: `compose.yml`

**Interfaces:**
- `Reconciler.Reconcile(ctx, DesiredInstance) (ObservedInstance, error)` is
  idempotent and persists a checkpoint before acknowledging completion.
- Desired instance includes `InstanceID`, `NodeID`, `Map`, `ClusterID`,
  `DesiredState`, `Ports`, `Storage`, and `Resources`.
- Docker socket proxy permits only required inspect/create/start/stop/remove/
  logs/events operations; no arbitrary exec or build endpoint.

- [ ] **Step 1: Write tests** for deterministic port allocation, idempotent reconcile, checkpoint recovery, and deletion of only platform-owned containers.
- [ ] **Step 2: Run tests** and verify the reconciler behavior is absent.
- [ ] **Step 3: Implement allocation transactions** with PostgreSQL uniqueness on node/protocol/port and a fixed range.
- [ ] **Step 4: Implement Docker container labels** (`ark.platform.instance-id`, node, cluster) and reconciliation from labels plus desired state.
- [ ] **Step 5: Implement heartbeat/reconnect with exponential backoff** and a durable last-ack checkpoint.
- [ ] **Step 6: Run two real instance containers** on one node and verify isolated ports, volumes, logs, and lifecycle state.

### Task 5: Control-plane jobs, API, WebSocket, and agent enrollment

**Files:**
- Create: `apps/control-plane/internal/api/router.go`
- Create: `apps/control-plane/internal/api/handlers.go`
- Create: `apps/control-plane/internal/jobs/queue.go`
- Create: `apps/control-plane/internal/events/hub.go`
- Create: `apps/control-plane/internal/agent/enrollment.go`
- Modify: `apps/control-plane/main.go`
- Create: `tests/controlplane/api_test.go`
- Create: `tests/controlplane/jobs_test.go`

**Interfaces:**
- Job queue functions: `Enqueue`, `Lease`, `Heartbeat`, `Complete`,
  `Fail`, `Retryable`; leases expire and are reclaimable.
- Agent protocol: `Enroll`, `Heartbeat`, `ReportObservedState`,
  `ReceiveDesiredState`, `AckJob`.
- API handlers map all errors to documented OpenAPI responses and publish
  event envelopes after committed mutations.

- [ ] **Step 1: Write failing tests** for job lease/reclaim, API validation, WebSocket cursor replay, and agent enrollment expiry.
- [ ] **Step 2: Run them against the current API** and verify failures are behavior-based.
- [ ] **Step 3: Implement the PostgreSQL-backed queue and worker** with transaction-safe leases.
- [ ] **Step 4: Implement chi routes from the OpenAPI contract** and replace the current placeholder instances response with real queries.
- [ ] **Step 5: Implement WebSocket replay/live fan-out** and outbound agent enrollment/heartbeat.
- [ ] **Step 6: Run API integration tests against a disposable PostgreSQL Compose service.**

### Task 6: Authentication, RBAC, secrets, and audit

**Files:**
- Create: `apps/control-plane/internal/auth/password.go`
- Create: `apps/control-plane/internal/auth/session.go`
- Create: `apps/control-plane/internal/auth/rbac.go`
- Create: `apps/control-plane/internal/secrets/store.go`
- Create: `apps/control-plane/internal/audit/audit.go`
- Create: `deploy/docker/secrets/README.md`
- Create: `tests/security/rbac_test.go`

**Interfaces:**
- Roles are exactly `admin`, `operator`, and `viewer` for this goal.
- `Authorize(principal, resource, action) error` returns a typed forbidden
  result for denied mutations.
- `SecretStore.Put/Get/Delete` encrypts values before database storage and
  never logs plaintext.

- [ ] **Step 1: Write failing tests** covering login/session expiry, CSRF rejection, Viewer mutation denial, Operator/Admin permissions, token hashing, and encrypted secret round-trip.
- [ ] **Step 2: Run tests** and capture expected failures.
- [ ] **Step 3: Implement cookie sessions, CSRF, role middleware, hashed API tokens, and one-use enrollment tokens.**
- [ ] **Step 4: Implement encrypted secret storage and audit entries for both allowed and denied actions.**
- [ ] **Step 5: Run security tests and inspect logs/database to prove secrets are absent.**

### Task 7: Web UI for operations

**Files:**
- Modify: `apps/web/package.json`
- Create: `apps/web/tailwind.config.ts`
- Create: `apps/web/src/api/client.ts`
- Create: `apps/web/src/auth/AuthContext.tsx`
- Create: `apps/web/src/hooks/useInstances.ts`
- Create: `apps/web/src/pages/Dashboard.tsx`
- Create: `apps/web/src/pages/Instances.tsx`
- Create: `apps/web/src/components/InstanceActions.tsx`
- Modify: `apps/web/src/main.tsx`
- Create: `tests/web/operations.test.tsx`

**Interfaces:**
- Native `fetch` client reads API base URL and credentials include cookies.
- `useInstances` returns `{items, loading, error, refresh}` and subscribes to
  WebSocket events with cursor reconnect.
- UI hides/ disables actions from the RBAC capability response and still
  handles a server-side 403.

- [ ] **Step 1: Write UI tests** for list loading, live status updates, Viewer action denial, and backup/restore confirmation.
- [ ] **Step 2: Run Bun tests** and verify the current placeholder UI fails these behaviors.
- [ ] **Step 3: Add Tailwind, routing, API client, auth context, hooks, and responsive dashboard.**
- [ ] **Step 4: Add EN/VI dictionaries and dark/light theme context.**
- [ ] **Step 5: Build the web image and run browser-level smoke tests through the reverse proxy.**

### Task 8: NFS, cluster transfer, optional S3/R2, and VPN documentation

**Files:**
- Create: `deploy/nfs/internal-compose.yml`
- Create: `deploy/nfs/external-mount.example.yaml`
- Create: `deploy/nfs/README.md`
- Create: `deploy/docker/backup/Dockerfile`
- Create: `deploy/docker/backup/backup.sh`
- Create: `docs/networking.md`
- Modify: `compose.yml`
- Create: `tests/cluster/cluster_smoke.sh`

**Interfaces:**
- `NFS_MODE=internal|external|disabled` selects cluster storage without
  changing the instance API.
- Backup backend is `local` by default and `s3`/`r2` when credentials are
  supplied via Docker secrets.
- Cluster transfer writes only to the configured cluster directory and never
  exposes save/config directories to another map.

- [ ] **Step 1: Write a two-node cluster smoke test** that checks shared cluster path, distinct map paths, and transfer file visibility.
- [ ] **Step 2: Run it without NFS** and verify it fails with a precise prerequisite message.
- [ ] **Step 3: Add internal NFS and external host-mount profiles** with explicit ownership/permissions.
- [ ] **Step 4: Add S3-compatible backup upload/download/verify** behind the same backup interface.
- [ ] **Step 5: Document Tailscale/WireGuard routing and firewall ports** for control-plane, agent, game, query, RCON, and NFS.
- [ ] **Step 6: Run the cluster smoke test on two Docker nodes or two isolated Compose projects.**

### Task 9: Reverse proxy, bootstrap, observability, and migration safety

**Files:**
- Modify: `deploy/caddy/Caddyfile`
- Modify: `deploy/nginx/default.conf`
- Modify: `scripts/bootstrap.sh`
- Create: `scripts/doctor.sh`
- Create: `docs/recovery.md`
- Create: `tests/deployment/fresh_install.ps1`
- Modify: `README.md`
- Modify: `docs/deployment.md`

- [ ] **Step 1: Write fresh-install checks** for required Docker features, kernel/network prerequisites, volume ownership, and secret file permissions.
- [ ] **Step 2: Run checks on the current host** and record each prerequisite result.
- [ ] **Step 3: Implement bootstrap/doctor/recovery commands** with no host Go/Bun dependency.
- [ ] **Step 4: Add Caddy internal TLS/LAN/domain examples and Nginx + Certbot profile documentation.**
- [ ] **Step 5: Add log rotation, resource limits, health/readiness endpoints, and migration rollback instructions.**
- [ ] **Step 6: Run both proxy profiles and the fresh-install script from a clean Docker volume set.**

### Task 10: Full acceptance matrix and final audit

**Files:**
- Create: `tests/acceptance/compose_acceptance.ps1`
- Create: `tests/acceptance/persistence.sh`
- Create: `tests/acceptance/rbac.sh`
- Create: `tests/acceptance/failure-reconcile.sh`
- Create: `docs/acceptance-matrix.md`

- [ ] **Step 1: Write acceptance commands** for every Definition of Done row, including the required evidence artifact.
- [ ] **Step 2: Run the matrix against a clean project name and fresh volumes.**
- [ ] **Step 3: Run persistence/recreate and backup/restore tests** with sentinel data.
- [ ] **Step 4: Run RBAC and failure/reconnect tests** with real containers and inspect database/audit rows.
- [ ] **Step 5: Run two-map/two-node cluster tests** and capture logs/ports/cluster files.
- [ ] **Step 6: Re-read the pasted requirements line by line, map each requirement to evidence, and report any unverified item instead of claiming completion.**
