# ARK ASA Admin Operations Console

## Goal

Turn the existing placeholder web page into a usable local operations console
for the ARK ASA platform. An administrator must be able to sign in, see the
health of the control plane, agent, and ARK instance, and request server
operations with role-aware access and an audit trail.

The first delivery is deliberately local-first: Docker Compose remains the
deployment boundary, PostgreSQL remains the source of durable state, and the
single local agent is responsible for applying queued operations to the ARK
container through the restricted Docker socket proxy.

## Current constraints

- The control plane currently exposes only health, readiness, an empty
  instances response, and an echo WebSocket.
- The database already contains users, sessions, instances, jobs, and audit
  tables, but there is no authentication or authorization implementation.
- The agent currently exposes health/readiness only and does not consume jobs.
- The web app is a static React/Bun bundle with no routing or API client.
- The running ARK service is the Compose service named `ark` and has the
  `ark-asa-platform` Compose labels needed for local discovery.

## User roles and security

Three roles are retained from the existing schema:

- `admin`: full access, including user management and server operations.
- `operator`: read access plus server operations and job actions.
- `viewer`: read-only dashboard, instances, jobs, and audit views.

The control plane will:

1. Bootstrap one admin from `ADMIN_USERNAME` and `ADMIN_PASSWORD` when the
   users table is empty. Passwords are stored with Argon2id; plaintext values
   are never persisted or returned.
2. Create cryptographically random database-backed sessions and set a
   `HttpOnly`, `SameSite=Lax` cookie. Session expiry and revocation are checked
   on every protected request.
3. Require a CSRF token for mutating browser requests. The token is returned
   only through the authenticated session endpoint and is stored hashed in the
   existing sessions table.
4. Enforce role permissions in the API, not only in the UI. Every mutating
   operation appends an audit record containing actor, action, resource, and
   structured metadata.
5. Use a separate `AGENT_TOKEN` for control-plane-to-agent job polling. This
   token is supplied by Compose and is never shown in the web app.

For local development, Compose may provide documented sample values, but the
`.env.example` file will continue to contain placeholders and the UI will show
a warning when the bootstrap password is unchanged.

## Backend API

The control plane will add these browser endpoints under `/api/v1`:

- `POST /auth/login` — validate credentials and create a session.
- `POST /auth/logout` — revoke the current session.
- `GET /auth/me` — return the current user and CSRF token.
- `GET /system/health` — aggregate control-plane, database, agent, and ARK
  status for the dashboard.
- `GET /instances` — return instance configuration plus observed status.
- `POST /instances/{id}/actions` — enqueue `start`, `stop`, or `restart` for
  operator/admin roles.
- `GET /jobs` — list recent jobs and their current status.
- `GET /audit` — list recent audit records for authenticated users.
- `GET /users` and `POST /users` — admin-only user listing and creation.
- `PATCH /users/{id}` — admin-only role/disabled-state updates.

Internal agent endpoints will not use browser sessions:

- `POST /internal/agent/lease` — atomically lease the next queued job.
- `POST /internal/agent/jobs/{id}/complete` — mark a job successful.
- `POST /internal/agent/jobs/{id}/fail` — release or fail a job with an error.
- `POST /internal/agent/heartbeat` — update the local node heartbeat.

The agent will poll for work, discover the Compose `ark` container through the
Docker API, execute the requested lifecycle operation, and report the result.
If the ARK container is absent, the job fails with a visible error rather than
pretending the action succeeded.

## Web experience

The app will become a responsive dark operations console:

- `/login`: username/password form, API error state, and bootstrap-password
  warning.
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

When no instance has been seeded yet, the dashboard will show a clear empty
state and a setup instruction. Compose bootstrap will create the local
`theisland` instance record so the existing ARK container is visible after the
first migration.

## Failure handling

- Invalid login returns a generic authentication error and does not reveal
  whether a username exists.
- Expired/revoked sessions return `401`; the web client redirects to `/login`.
- Forbidden role actions return `403` and are also hidden/disabled in the UI.
- Stale or failed jobs display their last error and remain auditable.
- Agent/Docker failures are returned as failed jobs and never reported as
  successful operations.
- Health aggregation treats control-plane/database failure as critical and
  agent/ARK failure as degraded, with timestamps for the last observation.

## Testing and verification

Before implementation code, tests will be added for:

- password hashing and credential validation;
- session expiry, logout, CSRF, and role middleware;
- login, `/auth/me`, protected instance actions, and audit writes;
- job lease/complete/fail transitions;
- web rendering of login, health states, role-gated actions, and API errors;
- Docker Compose configuration and an HTTP smoke test against the built web
  container.

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
