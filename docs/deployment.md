# Deployment and verification

## Ubuntu

Install Docker Engine and the Compose plugin, copy `.env.example` to `.env`,
change all passwords, then start the control plane with:

```bash
docker compose up -d postgres control-plane socket-proxy agent web
docker compose ps
curl --fail http://127.0.0.1:8080/healthz
curl --fail http://127.0.0.1:8080/readyz
```

The web operations console is available at `http://127.0.0.1:3000`. Use it to
review health, servers, jobs, audit records, nodes, and team access according
to the signed-in role.

The **Servers** page supports deleting a managed instance only after its agent
reports `stopped`. Deletion removes the database record and platform-owned
container on the next heartbeat, while the named persistence volumes remain
available for recovery.

With the stack running, verify the authenticated web surface and CSRF boundary
without printing the bootstrap secret:

```powershell
powershell -ExecutionPolicy Bypass -File tests/runtime/web_smoke.ps1
```

The smoke check logs in using `.secrets/admin_bootstrap_password`, verifies the
admin API pages, confirms a mutation without CSRF is rejected, and logs out.

For a fresh Ubuntu or Ubuntu-WSL node, `scripts/bootstrap.sh` performs the same
checks and starts the stack. Build the management CLI without installing Go on
the host with `scripts/install-arkctl.sh`, then use `.bin/arkctl status` or
`.bin/arkctl start` from the repository root.

To verify the remote-node enrollment path with a disposable non-game agent,
run:

```powershell
powershell -ExecutionPolicy Bypass -File tests/acceptance/node_enrollment.ps1
```

On native Windows PowerShell, run `scripts/bootstrap.ps1`; it performs the same
password generation, ACL protection, build, startup, and readiness checks.

The host must provide `/var/run/docker.sock`. The agent receives only the
restricted, allow-listed Docker API exposed by `socket-proxy`; it is not given
an unrestricted Docker socket.

To verify two managed instances share a node without sharing ports, labels, or
persistence boundaries, run the disposable acceptance probe (it removes its
own instances and containers when complete):

```powershell
powershell -ExecutionPolicy Bypass -File tests/acceptance/instance_isolation.ps1
```

## Agent enrollment

An administrator can register a node from the **Nodes** page. The API returns a
one-use enrollment token valid for 15 minutes; copy it into a Docker secret or
other protected file and configure a new agent with `NODE_ID`,
`NODE_ENROLLMENT_TOKEN_FILE`, and writable `AGENT_TLS_CERT_FILE`/
`AGENT_TLS_KEY_FILE` paths (the Compose agent state volume is mounted at
`/var/lib/ark-agent`). The agent generates its own private key and CSR,
then exchanges the token for a client certificate. The enrollment CA key stays
in its dedicated control-plane volume and is never mounted into the agent.

For RCON, create a host-side secret file and run with the RCON override:

```bash
mkdir -p secrets
printf '%s\n' 'replace-with-a-long-admin-password' > secrets/ark_admin_password
chmod 600 secrets/ark_admin_password
ARK_ADMIN_PASSWORD_SECRET_FILE="$PWD/secrets/ark_admin_password" \\
  docker compose -f compose.yml -f compose.rcon.yml --profile ark up -d ark
```

For both admin and server passwords, copy `compose.secrets.yml.example` to
`compose.secrets.yml`, set the two `*_SECRET_FILE` variables, and keep those
files outside Git. The base Compose file does not publish RCON and does not
require a password file.

## WSL2 / Docker Desktop

Docker Desktop must be running with the Linux engine enabled. Run commands from
PowerShell or inside an Ubuntu WSL distro; both use the same Docker context when
the Docker Desktop WSL integration is enabled. The `docker-desktop` internal
distro is not an interactive application distro and should not be modified.

## ARK runtime

The ARK image installs SteamCMD and GE-Proton at image-build time. The game files
are installed on first container start into the `ark-game` volume, while runtime
state is split into save/config/log/backups/cluster volumes. This separation is
intentional and is the persistence boundary for backup and restore tooling.

```bash
docker compose build ark
docker compose --profile ark up -d ark
docker compose logs -f ark
docker inspect --format '{{json .State.Health}}' ark-asa-platform-ark-1
```

Do not report ARK as ready until the health check is passing and the server log
shows the map has finished loading. Steam downloads and an actual server process
are required for that validation.
