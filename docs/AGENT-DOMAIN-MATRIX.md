# Agent Domain Matrix

This repository uses domain ownership to keep changes reviewable and prevent stacked PR scope drift.

| Domain | Primary paths | Issues | Boundary |
|---|---|---|---|
| Runtime / Proton | `deploy/docker/ark-runtime/*` | #1, #2, #4, #13 | Image, SteamCMD, Proton, launch, healthcheck |
| Agent / Reconciler | `apps/agent/*` | #3, #4, #8, #11 | Docker client, labels, lifecycle, observed state |
| Control Plane / Jobs | `apps/control-plane/*`, `db/queries/*` | #5 | API handlers, jobs, leases, events |
| Database / Migrations | `db/migrations/*`, generated DB code | #7, #10 | Schema, constraints, indexes, allocation |
| Security / Identity | control-plane auth and enrollment code | #6, #8 | Sessions, CSRF, RBAC, mTLS, secrets |
| ARK Operations | runtime shutdown/update orchestration | #2, #4, #10 | RCON, save verification, safe stop |
| Backup / Restore | agent backup worker and backup API contract | #10 | Archives, manifests, checksums, restore |
| Cluster / NFS | deployment and cluster health code | #11 | NFSv4, mounts, transfer health |
| Web UI | `apps/web/*` | #12 | React, Tailwind, UX, API client |
| OpenAPI / Contract | `api/openapi/*`, contract tests | all API changes | Source of truth and drift gate |
| Test / Acceptance | `tests/*`, CI checks | #14 | Integration, E2E, failure scenarios |
| PR / Dependency Review | review metadata and PR checks | all PRs | Base, scope, dependency and closure wording |

## Coordination Rules

1. One feature PR has one primary domain owner.
2. `api/openapi/*` changes require OpenAPI / Contract review before merge.
3. `db/migrations/*` changes require Database review and an Up/Down verification.
4. `db/queries/*` is shared: Control Plane owns behavior; Database owns schema compatibility.
5. Cross-domain work records the dependency in the PR body instead of adding unrelated files.
6. Use `Part of #N` for partial work and `Closes #N` only after all acceptance criteria pass.
7. Test / Acceptance runs with each feature; it is not deferred to the end of V1.

## Execution Order

The current parallel lanes are ARK Operations, Agent / Reconciler, Control Plane / Jobs, Security / Identity, and Test / Acceptance. OpenAPI / Contract gates every API or schema change before merge.
