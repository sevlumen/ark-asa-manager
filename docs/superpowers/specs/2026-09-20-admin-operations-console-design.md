# ARK ASA Admin Operations Console

## Goal

Turn the existing placeholder web page into a usable local operations console
for the ARK ASA platform. An administrator must be able to sign in, see the
health of the control plane, agent, and ARK instance, and request server
operations with role-aware access and an audit trail.

The first delivery is deliberately local-first: Docker Compose remains the
deployment boundary, PostgreSQL remains the source of durable state, and the
single local agent is responsible for applying queued operations to the ARK
container through the restricted Docker socket proxy. All API paths are
contract-first: the existing `api/openapi/openapi.yaml` paths are canonical;
new admin paths must be added to that contract before handlers or UI clients.

## Current constraints

- The control plane currently exposes only health, readiness, an empty
  instances response, and an echo WebSocket.
- The database already contains users, sessions, instances, jobs, and audit
  tables, but there is no authentication or authorization implementation.
- The agent currently exposes health/readiness only and does not consume jobs.
- The web app is a static React/Bun bundle with no routing or API client.
- The current local runtime has one ARK container, but future discovery must
  use stable `ark.platform.*` labels rather than the Compose service name.

## User roles and security

Three roles are retained from the existing schema:

- `admin`: full access, including user management and server operations.
- `operator`: read access plus server operations and job actions.
- `viewer`: read-only dashboard, instances, jobs, and audit views.

The control plane will:

1. Bootstrap one admin only when the users table is empty, using a one-time
   Docker secret mounted at `/run/secrets/admin_bootstrap_password` plus the
   non-secret `ADMIN_USERNAME` setting. The bootstrap secret is required for a
   fresh production database, is never accepted as a password environment
   variable, and is not needed after the first admin exists. Passwords are
   stored with Argon2id; plaintext values are never persisted or returned.
2. Create cryptographically random database-backed sessions and set a
   `HttpOnly`, `SameSite=Lax` cookie. The cookie is `Secure` whenever the
   public origin uses HTTPS; local HTTP development explicitly sets
   `Secure=false`. Session expiry and revocation are checked on every
   protected request.
3. Use a CSRF double-submit cookie. Login creates a random raw token, sets it
   in a non-HttpOnly `ark_csrf` cookie, and stores only its hash in the
   session. Mutating browser requests must copy the cookie into
   `X-CSRF-Token`. `GET /api/v1/me` returns principal data only; it never
   returns the raw token, so reloads use the retained cookie instead of
   reconstructing a secret from the database. A protected token-rotation
   endpoint is available when the cookie is absent.
4. Enforce role permissions in the API, not only in the UI. Every sensitive
   mutation, including a denied attempt, appends an audit record containing
   actor (or anonymous), action, resource, outcome, and structured metadata.
   Login failures use bounded per-IP and per-username throttling without
   revealing account existence.
5. Use the existing one-use node enrollment flow to issue a client certificate
   signed by the control-plane CA. The agent connects outbound over mTLS and
   authenticates with its enrolled certificate; there is no shared
   `AGENT_TOKEN` fallback.

For local development, Compose mounts an explicitly created local secret file
that is ignored by Git. Production uses Docker/Kubernetes secret management;
the repository contains only a setup example and never a default password.

## Backend API

The control plane will implement the existing browser contract under `/api/v1`:

- `POST /auth/login` — validate credentials and create a session.
- `POST /auth/logout` — revoke the current session.
- `GET /me` — return the current principal; it does not return a raw CSRF
  token.
- `GET /instances` — return instance configuration plus observed status.
- `POST /instances/{id}/actions/{action}` — enqueue the OpenAPI-defined action
  for operator/admin roles. The backend retains the complete OpenAPI action
  set, including update, backup, and restore; the first UI may expose only
  start/stop/restart while those other actions receive their own pages.
- `GET /jobs/{id}` — return a durable job and its status.
- `GET /audit` — list recent audit records for authenticated users.
- `GET /nodes` and node mutation routes — admin-only node management, matching
  the existing OpenAPI contract.

Every response carries a request ID (generated or propagated from
`X-Request-ID`) and failures use the existing OpenAPI error shape. List
endpoints use cursor pagination for jobs, audit, users, nodes, and instances;
the cursor is opaque and stable for its query. The OpenAPI contract is updated
before adding any list or CSRF-rotation endpoint not already present.

Any required admin additions not currently in OpenAPI (user listing/creation,
job listing, health aggregation, and CSRF rotation) will be added to
`api/openapi/openapi.yaml` first with response schemas, then implemented and
covered by the contract test. No route is invented only in the design or UI.

Internal agent endpoints will not use browser sessions or the public listener.
They run on a separate mTLS-only HTTP listener bound inside the Compose
network; no host port is published for it:

- `POST /internal/agent/lease` — atomically lease the next queued job.
- `POST /internal/agent/jobs/{id}/complete` — mark a job successful.
- `POST /internal/agent/jobs/{id}/fail` — release or fail a job with an error.
- `POST /internal/agent/heartbeat` — update the local node heartbeat.

The mTLS listener authorizes the enrolled node identity before any internal
handler runs. The agent, not the control plane, opens the outbound connection
and polls for work. It discovers managed containers by stable labels such as
`ark.platform.instance-id`, `ark.platform.node-id`, and
`ark.platform.cluster-id`; it never relies on a Compose service name. It then
executes the requested lifecycle operation and reports the result.
If the ARK container is absent, the job fails with a visible error rather than
pretending the action succeeded.

In the local Compose profile, the private listener is reachable only on the
Compose network. For multi-node deployments, the same listener is bound to a
dedicated LAN/VPN address reachable by enrolled agents over outbound mTLS;
firewall rules allow only the agent network, and the public reverse proxy never
forwards `/internal/*`. This preserves private ingress locally without making
the V1 multi-node transport depend on host-local routing.

## Web experience

The app will become a responsive dark operations console:

- `/login`: username/password form and API error state. Bootstrap-secret setup
  is an operator/deployment concern, not a password embedded in the UI.
- `/`: dashboard with service health cards, ARK instance card, player/server
  metadata, quick actions, recent jobs, and recent audit activity.
- `/servers`: instance list and action controls with confirmation for stop and
  restart.
- `/jobs`: filterable job status list with failure details.
- `/audit`: recent administrative activity.
- `/users`: admin-only user and role management.

The client will use a small typed API module and session context. It will not
store credentials or session tokens in localStorage. The current visual
direction is dark graphite surfaces, amber ARK accents, green health signals,
and high-contrast responsive cards.

The browser uses same-origin relative `/api` requests. The web container or
the selected Caddy/Nginx profile proxies `/api` and `/api/v1/ws` to the
control-plane, so the default `http://localhost:3000` dashboard does not rely
on permissive cross-origin cookies. If a direct cross-origin mode is retained
for diagnostics, it must use an explicit allowed-origin list and credentials;
wildcard CORS is forbidden.

Instance status is realtime: the UI connects to the OpenAPI WebSocket with a
cursor, replays missed durable events after reconnect, and falls back to
bounded polling only while the socket is unavailable. The echo loop is
replaced before this delivery is considered complete.

When no instance has been seeded yet, the dashboard will show a clear empty
 state and a setup instruction. A separate idempotent post-migration
bootstrap/reconcile step may register the local instance after the schema is
ready; migrations remain deterministic and contain no deployment-specific
`theisland` seed row.

## Failure handling

- Invalid login returns a generic authentication error and does not reveal
  whether a username exists.
- Expired/revoked sessions return `401`; the web client redirects to `/login`.
- Forbidden role actions return `403` and are also hidden/disabled in the UI.
- Denied mutations are visible in audit history with a denied outcome, while
  the response still uses the documented `403` error contract.
- Stale or failed jobs display their last error and remain auditable.
- Agent certificate expiry, enrollment failure, or mTLS rejection marks the
  node unavailable; Docker failures are returned as failed jobs and never
  reported as successful operations.
- Health aggregation treats control-plane/database failure as critical and
  agent/ARK failure as degraded, with timestamps for the last observation.

## Testing and verification

Before implementation code, tests will be added for:

- password hashing and credential validation;
- session expiry, logout, CSRF, and role middleware;
- login, `/me`, protected instance actions, and audit writes;
- OpenAPI route/schema parity for every implemented browser and internal
  endpoint;
- bootstrap from a Docker secret with no password environment fallback;
- enrollment expiry, certificate identity, mTLS rejection, and private
  listener isolation;
- WebSocket cursor replay/reconnect and same-origin proxy behavior;
- login throttling, denied-action audit, request IDs, error envelopes, and
  cursor pagination;
- job lease/complete/fail transitions;
- web rendering of login, health states, role-gated actions, and API errors;
- Docker Compose configuration and an HTTP smoke test against the built web
  container.
- production HTTPS cookie flags versus local HTTP mode, with no wildcard CORS.

Verification for the completed change will include the Go test suite, web
build, runtime contract, `docker compose config --quiet`, migration startup,
health endpoints, and a real login/dashboard smoke test against
`http://localhost:3000`.

## Out of scope for this delivery

- Public internet exposure, TLS certificates, SSO/OAuth, and multi-factor
  authentication.
- Player-level game administration through RCON.
- Multi-node scheduling beyond the existing single local agent.
- Storing production secrets in the repository.
