# Repository Guidelines

## Project Structure & Module Organization

- `apps/control-plane/`: Go REST/WebSocket API, authentication, and durable jobs.
- `apps/agent/`: Go node agent and Docker reconciliation.
- `apps/web/`: React/TypeScript console with Tailwind and Vite; source and styles live in `src/`.
- `cmd/arkctl/`: Go operational CLI.
- `api/openapi/openapi.yaml`: API contract; keep handlers and clients aligned with it.
- `db/`: Goose migrations, sqlc queries, and generated Go code. Update queries and regenerate bindings rather than editing generated files manually.
- `deploy/`: Docker runtime scripts and reverse-proxy configuration. `tests/` contains contract, runtime, and acceptance suites; `docs/` contains deployment guidance and design specifications.

## Build, Test, and Development Commands

Run from the repository root unless noted:

- `pwsh -File scripts/test.ps1`: runs Go suites, the web build, and proxy/cleanup contracts.
- `make test`: runs Go suites in Docker. The workspace has multiple modules; root-level `go test ./...` is unsupported.
- `docker compose config --quiet`: validates Compose configuration.
- `make build`: builds control-plane, agent, web, and ARK images.
- `make up`: starts the management services; web listens at `http://localhost:3000`.
- `docker compose --profile ark up -d ark`: starts the optional game runtime; initial downloads can be lengthy.
- In `apps/web/`, run `bun install --frozen-lockfile`, then `bun run dev` or `bun run build`.

## Coding Style & Naming Conventions

Format Go with `gofmt`; use idiomatic exported names and focused functions. Follow existing TypeScript/TSX and PowerShell indentation. Name Go tests `*_test.go` with `Test...` functions. Add numbered, descriptive SQL migrations with explicit Up/Down sections. Preserve dependency lockfiles.

## Testing Guidelines

Use Go's standard testing package and PowerShell contract/acceptance scripts. Add behavioral regression coverage for changed lifecycle, authorization, allocation, and persistence logic. No enforced numeric coverage threshold is currently defined. Run `pwsh -File scripts/acceptance.ps1` against a prepared local stack; inspect prerequisites before executing tests that create containers or data. A successful build does not prove actual ARK readiness.

## Commit & Pull Request Guidelines

Follow scoped commit subjects such as `fix(runtime): ...`, `feat(monitoring): ...`, and `style(web): ...`. PRs should describe the problem, resulting behavior, linked issues, verification results, and remaining limitations. Include desktop/mobile screenshots for UI changes. Use `Closes #N` only when all acceptance criteria are met; otherwise use `Part of #N`.

## Security & Configuration

Bootstrap from `.env.example` using `scripts/bootstrap.sh` or `scripts/bootstrap.ps1`. Keep credentials in file-backed secrets and out of commits, logs, and process arguments. Preserve persistent volumes during troubleshooting; avoid `docker compose down -v` on existing data.

## Domain Agent Coordination

Domain ownership and cross-agent gates are defined in [`docs/AGENT-DOMAIN-MATRIX.md`](docs/AGENT-DOMAIN-MATRIX.md). Role layers live in `.codex/agents/`. Keep feature changes inside one primary domain, record cross-domain dependencies in the PR, and run OpenAPI / Contract review before merging API or schema changes.

The required delivery pipeline is documented in [`docs/agent-workflow.md`](docs/agent-workflow.md). Use `.agents/task-handoff.yml` for durable handoffs and the `luna-ba → luna-planner → luna-dev → luna-qc → luna-reviewer → luna-release` gates. Add `luna-security` for security-sensitive changes and call specialists only when their domain is involved.
