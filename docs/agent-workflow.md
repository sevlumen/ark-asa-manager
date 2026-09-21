# Luna Agent Workflow

## Pipeline

```text
BA → Planner → Dev → QC → Reviewer (+ Security when required) → Release → Merge
```

Findings return to Dev. After fixes, repeat QC, independent review and any required
security review before Release. Specialists advise; they never replace pipeline gates.

## Runnable Roles

`.codex/config.toml` registers seven primary roles and five specialists. Each
`.codex/agents/luna-*.toml` uses `gpt-5.6-luna` and points its developer instructions
to the corresponding `.agents/` Markdown contract. Restart the Codex session after
configuration changes. A prompt such as "Delegate this requirement to luna-ba"
asks the lead to spawn that role; `@luna-ba` is not a guaranteed CLI command.

The lead is the parent session. It assigns tasks, persists returned reports and
checks provenance. Child agents do not spawn further agents. Six concurrent slots
are sufficient: seven primary roles are pipeline stages, not seven resident workers.
Release completed sessions before starting subsequent stages.

| Roles | Local access | Responsibility |
|---|---|---|
| BA, Planner | Read-only | Return requirements/plans for the lead to record |
| Dev | Workspace-write | Approved implementation and unit tests |
| QC | Workspace-write | Verification and test code only |
| Reviewer, Security, Release | Read-only | Findings, assessment and release evidence |
| Runtime, DB, Agent, Web, Infra specialists | Read-only | Domain advice to the assigned owner |

Filesystem sandboxing does not enforce test-only paths for QC or authorize external
writes. The lead checks every diff against the handoff's allowed/forbidden paths.
No role gains push/merge/deployment authority from a passed gate.

Old domain role layers are archived under `.agents/legacy-domain-roles/` to prevent
Codex from auto-discovering conflicting implementation/review roles. The domain
matrix remains the source for file ownership.

## Stages and Gates

| Stage | Owner | Required output | Gate |
|---|---|---|---|
| `BA_READY` | `luna-ba` | Requirement | Scope, actors, rules, acceptance criteria are explicit |
| `PLAN_READY` | `luna-planner` | Technical Plan | Files, dependencies, tests, rollout and rollback are named |
| `IMPLEMENTED` | `luna-dev` | Implementation Summary | Scope respected; deviations recorded |
| `QC_PASS` | `luna-qc` | Acceptance Matrix | Happy path, failures, regression and persistence tested |
| `REVIEW_PASS` | `luna-reviewer` | Review Findings | No unresolved blocker/high finding |
| `SECURITY_PASS` | `luna-security` | Security Review | Required for any security trigger listed below |
| `MERGE_READY` | `luna-release` | Release Gate | Base, dependencies, evidence, issue linkage and comments verified |
| `MERGED` | Lead | Merge record | PR is merged and issue wording is accurate |

All gates start `PENDING`. BA READY maps to `BA_READY`; Planner READY maps to
`PLAN_READY`; Dev DONE maps to `IMPLEMENTED` only with implementation evidence.
QC PASS maps to `QC_PASS`; CONDITIONAL and FAIL both block release.
Reviewer APPROVE maps to `REVIEW_PASS` only when required findings are resolved.
Security PASS maps to `SECURITY_PASS`; Release MERGE_READY maps to `MERGE_READY`.
Missing outputs, BLOCKED, NEEDS_CONFIRMATION or CHANGES_REQUIRED never satisfy a gate.
No task may move directly from `IMPLEMENTED` to `MERGED`.

BLOCKER/HIGH findings must be RESOLVED with evidence. MEDIUM/LOW findings must be
resolved or explicitly ACCEPTED with a rationale by the reviewer and release owner.
Blank or unknown decisions block release.
Each resolved/accepted finding has its own nonempty `evidence` list and rationale.
For ACCEPTED, `accepted_by` must list exactly the current Reviewer and Release
actor IDs; their source reports must confirm that acceptance.

## Independent Evidence and Invalidation

Record actual session/thread IDs (or accountable human identities), not just role
names. If a child cannot inspect its ID, the lead records the actual ID returned
by the spawn tool; never fabricate one. List every implementation author in
`implementation.actor_ids`, including
agents that fix findings. QC, Reviewer, Security assessor and Release must use
separate sessions from each other and from every implementation author.
Never relabel a Dev session as Reviewer. The lead preserves each report's source;
it cannot create a missing independent approval.

Record full 40-character `head_sha` and `base_sha` on each verification gate.
Use the PR head and current target-branch tip, not a synthetic CI merge commit.
Before review, freeze a clean candidate commit. Tests on uncommitted edits are
provisional; commit them and verify the final candidate.

Any head or base SHA change invalidates QC, Review, Security and Release gates,
including changes made by QC, rebases and retargeting. Set them back to PENDING
and rerun against the new pair. Preserve old reports as history, not current proof.
Requirement/scope changes also invalidate BA and Planner.

Evidence must identify the command or review, result, environment, actor, SHA pair
and a retrievable log/report/artifact. Include desktop/mobile screenshots for UI
changes and redact secrets. A successful build is not runtime acceptance.

## Security Assessment

Mandatory triggers: auth, passwords, secrets, RCON, mTLS, sessions, CSRF, RBAC,
API tokens, Docker socket, filesystem permissions, public networking and backup
encryption. Trigger detection is based on behavior and diff, not only paths.
Record matching triggers; any trigger requires `security.required: true`.

Only an independent security assessor can choose `required: false` and a gate
status of `N/A`, with a nonempty `na_reason` and a report examining the diff.
An unassessed `null` never passes. N/A evidence must match the current SHA pair.
Security returns findings only and must not fix feature or test code.

## Ownership

Use [`AGENT-DOMAIN-MATRIX.md`](AGENT-DOMAIN-MATRIX.md) for file boundaries. A task has one primary owner. Cross-domain changes are recorded as dependencies and pass the OpenAPI / Contract gate when API or schema contracts change.

## Handoff

Copy `.agents/task-handoff.yml` for each task; do not fill out the shared template.
The lead is the sole writer of that task's gate record. Store completed handoffs
and reports as external PR attachments or CI artifacts identified by issue and SHA.
Do not commit a final handoff containing its own commit SHA: that creates an
impossible self-reference. Draft requirement/plan documents may live in the repo.

Each handoff records scope, dependencies, acceptance, stage, authors, SHA pair,
findings, blockers and evidence links. Empty templates must fail release validation.

Scope paths are repository-relative and use forward slashes. Plain paths match
themselves and descendants; `*` and `?` match within one path component, while
`**` as an entire component matches recursively. Forbidden patterns win.
Absolute paths, backslashes, dot/traversal components and malformed patterns fail.
The lead compares `changed_files` with the actual Git diff; the validator checks
the declared list only.

From the repository root, validate a completed release handoff (replace artifact
path and SHAs with independently verified PR refs):

```powershell
$workflowGoWork = $env:GOWORK
Push-Location tests/contracts
try {
  $env:GOWORK = 'off'
  go run ./cmd/luna-gate -handoff 'D:/review-artifacts/handoff.yml' -head 'PR_HEAD_SHA' -base 'TARGET_SHA'
  if ($LASTEXITCODE -ne 0) { throw 'Handoff is not release-ready' }
} finally {
  $env:GOWORK = $workflowGoWork
  Pop-Location
}
```

The validator checks structure, statuses, distinct declared actors, SHA matching,
declared file scope, finding evidence/acceptance and the security N/A decision.
It cannot authenticate identities, fetch
evidence, infer security triggers, or prove tests ran. Release must verify those
facts against source reports and GitHub. A validator success alone is not approval.
Use current remote SHAs obtained independently of the handoff.

CI runs validator regression tests through the existing Go contract suite.
It does not automatically approve each PR or configure branch protection.
Release still checks all independent reports, actual CI results, mergeability,
unresolved threads and dependency order. Missing evidence means NOT_READY.

## Domain Routing

| Issue scope | Specialist / gate |
|---|---|
| #2 graceful shutdown | Runtime + Security |
| #3 reconciler | Agent + Security |
| #4 update orchestration | Runtime; Security when RCON/secrets are involved |
| #5 durable jobs | DB |
| #6 authentication | Security |
| #7 allocator | DB + concurrency QC |
| #8 enrollment/mTLS | Agent + Security |
| #10 backup/restore | Runtime/DB as needed; Security for permissions/encryption |
| #11 NFS | Infra + Security |
| #12 UI | Web; Security for session/auth changes |
| #14 acceptance | QC |

Issue numbers describe routing, not completion status. Check current issue criteria.
For API/schema work, Planner owns the OpenAPI-first plan, Dev updates generated
artifacts, and Reviewer explicitly records the OpenAPI / Contract check. DB specialist
advice and migration Up/Down evidence are required for migration changes.

## Kanban

Normal flow: `Backlog → Ready → Running → Review → Merged → Archived`.
Blocked is an exceptional state from any active stage, with owner and next action;
it returns to that stage after resolution. Review requires tests and a changed-file list.

## Configuration Reference

Role layers use `developer_instructions` and `sandbox_mode` following
[official Codex subagent documentation](https://learn.chatgpt.com/docs/agent-configuration/subagents).
See [CODEX-NAVIGATION-GUIDE.md](CODEX-NAVIGATION-GUIDE.md) for repository entry points.
