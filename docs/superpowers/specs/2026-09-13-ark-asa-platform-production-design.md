# ARK ASA Platform Production Design

## Goal

Build a self-hosted ARK: Survival Ascended platform that can bootstrap on a
fresh Ubuntu host and operate one or more Docker-managed ARK instances across
multiple nodes, with persistent state, cluster transfer, control-plane, agent,
API, UI, security, backup, and documented recovery.

The pasted requirements are the authoritative specification:
`C:\Users\quang\.codex\attachments\b4b91d52-4f73-47b2-8239-205b70127866\pasted-text-1.txt`.

## Non-negotiable invariants

1. Docker/Compose is the execution boundary. Host installation is limited to
   Docker Engine/Compose, network/storage prerequisites, and a thin CLI wrapper.
2. A mock, fake health response, or simulated game process cannot satisfy an
   acceptance criterion. ARK readiness requires the real server process and a
   passing container health check.
3. Game files, save, config, logs, backups, and cluster data are separate
   persistence boundaries. Recreating a container must not remove any of them.
4. The control-plane never receives the host Docker socket. The agent uses a
   restricted Docker socket proxy and authenticates outbound to the control
   plane using mTLS plus an enrollment token.
5. PostgreSQL is the only control-plane database. SQLite is not a fallback.
6. API contracts are written before handlers and generated clients/types are
   not hand-maintained duplicates.

## Recommended architecture

The platform uses a central Go control-plane and one Go agent per Docker node.
The control-plane owns desired state, jobs, users, audit records, node
enrollment, and event fan-out in PostgreSQL. An agent maintains a local
checkpoint, connects outbound over mTLS, observes the Docker Engine through a
socket proxy, and reconciles instance specs into non-root ARK containers.

Each instance has a stable ID, map, cluster ID, port allocation, storage
backend, resource limits, and desired state. Local storage is represented by
named volumes or host bind mounts. NFS is mounted on the node and exposed to
the agent/runtime as a dedicated cluster-data path; it is never mixed with
save/config/log paths. Internal NFS is a Compose profile; external NFS is a
documented host mount profile. Tailscale/WireGuard remains a host networking
prerequisite, while all platform control traffic remains inside the same mTLS
protocol.

The ARK image is built from Ubuntu, installs SteamCMD and a pinned Proton
release with checksum verification, creates a non-root `ark` user, and defers
the large ASA game download to a persistent game volume at first start. The
build emits progress and has bounded retries. The runtime receives map,
crossplay, PvE, player limit, BattlEye, cluster, and port settings from an
instance manifest. The default map is `TheIsland_WP` and the default player
limit is 10.

## Control-plane boundaries

- `api/openapi/openapi.yaml` is design-first and defines auth, nodes,
  instances, jobs, backups, audit, and WebSocket event envelopes.
- PostgreSQL migrations are applied by a dedicated Docker migration job before
  the control-plane starts. Goose metadata is stored in the same database.
- `instances` stores desired state; `instance_status` stores observed state;
  `jobs` stores durable commands with lease/attempt fields; `audit_log` stores
  immutable actor/action/resource records.
- All mutating handlers require an authenticated principal and a role check.
  Viewer can read only. Operator can operate instances and backups. Admin can
  manage nodes, users, secrets, and configuration.
- WebSocket events are emitted after durable state changes and include a
  monotonically increasing event ID so clients can reconnect from a cursor.

## Security and secrets

Session cookies are HttpOnly/Secure/SameSite and protected by CSRF tokens for
browser mutations. API tokens are hashed at rest and shown once. Enrollment
tokens are one-use and expire. Agent certificates are issued by the
control-plane CA and rotated before expiry. Docker secrets provide runtime
passwords; encrypted secret payloads use an application key supplied outside
the repository. Audit logging records denied mutations as well as successful
ones without storing plaintext secrets.

## Acceptance gates

The implementation is complete only when the following evidence exists:

- A fresh Ubuntu Docker host runs the bootstrap script and starts PostgreSQL,
  control-plane, agent, web, and a real TheIsland instance.
- Recreate/restart preserves a sentinel save/config/log/backup file.
- `arkctl` creates, verifies, restores, updates, restarts, and reports the
  instance through Docker and the API.
- Two instances on one node receive non-overlapping ports and independent
  volumes.
- Two agent nodes can run two maps with the same cluster backend and exchange
  cluster data through NFS.
- Disconnect/reconnect causes reconciliation from durable desired state.
- Viewer receives 403 for mutations while Operator/Admin receive only their
  permitted actions.
- Both Nginx and Caddy profiles route API and UI successfully; HTTPS is tested
  with a real TLS handshake.
- Automated tests cover API authorization, job leasing, port allocation,
  backup verification/restore safety, agent reconnect, and Compose smoke flows.

## Explicit exclusions

Advanced scheduling, auto-rebalance, enterprise staged rollout, multi-person
approval, OIDC/OAuth/TOTP, TPM/HSM, NFS HA automation, advanced alerting, and
full enterprise observability remain outside this goal.
