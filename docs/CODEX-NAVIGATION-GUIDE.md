# Codex Navigation Guide

Read root AGENTS.md, then this guide and [agent-workflow.md](agent-workflow.md).
The active roles are the twelve Luna configs in `.codex/agents/`; Markdown
contracts live in `.agents/`. Legacy domain configs are references only.

## Entry Points

| Surface | Start here |
|---|---|
| API contracts | `api/openapi/openapi.yaml`, `tests/contracts/` |
| Control plane / jobs / identity | `apps/control-plane/`, `db/queries/` |
| Agent / Docker reconciliation | `apps/agent/` |
| Database | `db/migrations/`, `db/sqlc.yaml`, `db/generated/` |
| Runtime / Proton / RCON | `deploy/docker/ark-runtime/`, `tests/runtime/` |
| Web UI | `apps/web/src/`, `apps/web/package.json` |
| Deployment / verification | `compose.yml`, `scripts/`, `.github/workflows/ci.yml` |
| Workflow gate validator | `tests/contracts/workflow/`, `tests/contracts/cmd/luna-gate/` |

Use `rg --files` to locate files and `rg -n 'symbol' apps tests` for focused
searches. Check the current OpenAPI/schema before changing handlers or clients.
Do not manually edit generated database bindings.

## Ownership and Review Packet

Use [AGENT-DOMAIN-MATRIX.md](AGENT-DOMAIN-MATRIX.md) for file boundaries and the
Luna workflow for role authority. Assign disjoint worktrees/write scopes to
parallel implementation tasks; return specialist advice to the task owner.

Every review packet includes issue/requirement/plan links, target branch, full
head/base SHAs, the actual changed-file list, allowed/forbidden scope, author
session IDs, test reports and remaining findings. Include desktop/mobile
screenshots for UI changes. Report unverified checks explicitly.

Before release, compare the packet with the current PR refs and source reports.
A checkbox, successful build or structural handoff validation alone is not
independent review evidence. Follow the workflow's gate invalidation rules.
