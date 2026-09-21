# Luna Agent Workflow

## Pipeline

```text
BA → Planner → Dev → QC → Reviewer → Dev fixes → QC regression → Release → Merge
```

Security-sensitive work adds `luna-security` beside `luna-reviewer`; specialist agents advise the pipeline and do not replace it.

## Stages and Gates

| Stage | Owner | Required output | Gate |
|---|---|---|---|
| `BA_READY` | `luna-ba` | Requirement | Scope, actors, rules, acceptance criteria are explicit |
| `PLAN_READY` | `luna-planner` | Technical Plan | Files, dependencies, tests, rollout and rollback are named |
| `IMPLEMENTED` | `luna-dev` | Implementation Summary | Scope respected; deviations recorded |
| `QC_PASS` | `luna-qc` | Acceptance Matrix | Happy path, failures, regression and persistence tested |
| `REVIEW_PASS` | `luna-reviewer` | Review Findings | No unresolved blocker/high finding |
| `SECURITY_PASS` | `luna-security` | Security Review | Required for auth, secrets, RCON, mTLS, Docker socket or public networking |
| `MERGE_READY` | `luna-release` | Release Gate | Base, dependencies, evidence, issue linkage and comments verified |
| `MERGED` | Lead | Merge record | PR is merged and issue wording is accurate |

No task may move directly from `IMPLEMENTED` to `MERGED`.

## Ownership

Use [`AGENT-DOMAIN-MATRIX.md`](AGENT-DOMAIN-MATRIX.md) for file boundaries. A task has one primary owner. Cross-domain changes are recorded as dependencies and pass the OpenAPI / Contract gate when API or schema contracts change.

## Handoff

Use `.agents/task-handoff.yml`. Every handoff records issue, scope, dependencies, acceptance criteria, current stage, changed files, evidence, and blockers. The handoff is the durable record; chat text alone is not evidence.

## Kanban

Cards use `Backlog → Ready → Running → Review → Blocked → Merged → Archived`. A card cannot enter `Review` without tests and a changed-file list. A blocked card records an owner and next action.
