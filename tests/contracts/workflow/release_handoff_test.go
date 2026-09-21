package workflow

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const validHandoff = `schema_version: 1
task:
  issue: "#42"
  title: Ship release handoff validator
requirements:
  source: luna-ba
  status: BA_READY
  actor_id: ba-1
  evidence: [requirements.md]
plan:
  source: luna-planner
  status: PLAN_READY
  actor_id: planner-1
  evidence: [plan.md]
scope:
  allowed: [tests/contracts/workflow, tests/contracts/cmd/luna-gate]
  forbidden: [apps/control-plane]
acceptance: [validator accepts a complete handoff]
current_stage: RELEASE
head_sha: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
base_sha: bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
implementation:
  status: IMPLEMENTED
  actor_ids: [dev-1]
  head_sha: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
  evidence: [implementation.md]
gates:
  qc:
    status: QC_PASS
    actor_id: qc-1
    head_sha: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
    base_sha: bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
    evidence: [qc.log]
  review:
    status: REVIEW_PASS
    actor_id: review-1
    head_sha: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
    base_sha: bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
    evidence: [review.md]
  security:
    status: SECURITY_PASS
    actor_id: security-1
    head_sha: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
    base_sha: bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
    evidence: [security.md]
  release:
    status: MERGE_READY
    actor_id: release-1
    head_sha: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
    base_sha: bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
    evidence: [release.md]
security:
  required: true
  triggers: [production]
  na_reason: ""
dependencies:
  required: []
  blocks: []
owner: release-team
branch: feat/release
worktree: D:/Code/ark-asa-manager
changed_files: [tests/contracts/workflow/release_handoff.go]
evidence: [ci.log]
blockers: []
findings: []
`

func TestValidateReleaseHandoff(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(string) string
		wantErr bool
	}{
		{name: "valid passes", mutate: func(s string) string { return s }},
		{name: "security not applicable passes", mutate: func(s string) string {
			s = strings.Replace(s, "required: true\n  triggers: [production]", "required: false\n  triggers: []", 1)
			s = strings.Replace(s, "na_reason: \"\"", "na_reason: no security trigger", 1)
			return strings.Replace(s, "status: SECURITY_PASS", "status: N/A", 1)
		}},
		{name: "stale head rejected", mutate: func(s string) string {
			return strings.Replace(s, "head_sha: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "head_sha: cccccccccccccccccccccccccccccccccccccccc", 1)
		}, wantErr: true},
		{name: "stale base rejected", mutate: func(s string) string {
			return strings.Replace(s, "base_sha: bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "base_sha: cccccccccccccccccccccccccccccccccccccccc", 1)
		}, wantErr: true},
		{name: "self review rejected", mutate: func(s string) string { return strings.Replace(s, "actor_id: review-1", "actor_id: dev-1", 1) }, wantErr: true},
		{name: "second implementation author cannot review", mutate: func(s string) string {
			s = strings.Replace(s, "actor_ids: [dev-1]", "actor_ids: [dev-1, dev-2]", 1)
			return strings.Replace(s, "actor_id: review-1", "actor_id: dev-2", 1)
		}, wantErr: true},
		{name: "conditional qc rejected", mutate: func(s string) string { return strings.Replace(s, "status: QC_PASS", "status: CONDITIONAL", 1) }, wantErr: true},
		{name: "blank readiness template rejected", mutate: func(s string) string {
			return "schema_version: 1\ntask: {}\nrequirements: {}\nplan: {}\nscope: {allowed: [], forbidden: []}\nacceptance: []\ncurrent_stage: RELEASE\n"
		}, wantErr: true},
		{name: "security trigger cannot be not applicable", mutate: func(s string) string {
			s = strings.Replace(s, "required: true\n  triggers: [production]", "required: false\n  triggers: [production]", 1)
			s = strings.Replace(s, "status: SECURITY_PASS", "status: N/A", 1)
			return strings.Replace(s, "na_reason: \"\"", "na_reason: no security trigger", 1)
		}, wantErr: true},
		{name: "empty N/A rationale rejected", mutate: func(s string) string {
			s = strings.Replace(s, "required: true\n  triggers: [production]", "required: false\n  triggers: []", 1)
			return strings.Replace(s, "status: SECURITY_PASS", "status: N/A", 1)
		}, wantErr: true},
		{name: "missing security required rejected", mutate: func(s string) string {
			s = strings.Replace(s, "required: true\n  triggers: [production]", "required: false\n  triggers: []", 1)
			s = strings.Replace(s, "status: SECURITY_PASS", "status: N/A", 1)
			s = strings.Replace(s, "na_reason: \"\"", "na_reason: no security trigger", 1)
			return strings.Replace(s, "  required: false\n", "", 1)
		}, wantErr: true},
		{name: "null security required rejected", mutate: func(s string) string {
			s = strings.Replace(s, "required: true\n  triggers: [production]", "required: null\n  triggers: []", 1)
			s = strings.Replace(s, "status: SECURITY_PASS", "status: N/A", 1)
			return strings.Replace(s, "na_reason: \"\"", "na_reason: no security trigger", 1)
		}, wantErr: true},
		{name: "per-gate stale review head rejected", mutate: func(s string) string {
			return strings.Replace(s, "status: REVIEW_PASS\n    actor_id: review-1\n    head_sha: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "status: REVIEW_PASS\n    actor_id: review-1\n    head_sha: cccccccccccccccccccccccccccccccccccccccc", 1)
		}, wantErr: true},
		{name: "per-gate stale review base rejected", mutate: func(s string) string {
			return strings.Replace(s, "status: REVIEW_PASS\n    actor_id: review-1\n    head_sha: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n    base_sha: bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "status: REVIEW_PASS\n    actor_id: review-1\n    head_sha: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n    base_sha: cccccccccccccccccccccccccccccccccccccccc", 1)
		}, wantErr: true},
		{name: "duplicate gate actor rejected", mutate: func(s string) string { return strings.Replace(s, "actor_id: release-1", "actor_id: qc-1", 1) }, wantErr: true},
		{name: "empty gate evidence rejected", mutate: func(s string) string { return strings.Replace(s, "evidence: [review.md]", "evidence: []", 1) }, wantErr: true},
		{name: "resolved finding needs rationale", mutate: func(s string) string {
			return strings.Replace(s, "findings: []", "findings: [{severity: HIGH, status: RESOLVED, rationale: \"\"}]", 1)
		}, wantErr: true},
		{name: "resolved high finding with evidence passes", mutate: func(s string) string {
			return strings.Replace(s, "findings: []", "findings: [{severity: HIGH, status: RESOLVED, rationale: fixed, evidence: [fix.md]}]", 1)
		}},
		{name: "accepted low finding with evidence and both actors passes", mutate: func(s string) string {
			return strings.Replace(s, "findings: []", "findings: [{severity: LOW, status: ACCEPTED, rationale: accepted risk, evidence: [risk.md], accepted_by: [review-1, release-1]}]", 1)
		}},
		{name: "out of scope changed file rejected", mutate: func(s string) string {
			return strings.Replace(s, "changed_files: [tests/contracts/workflow/release_handoff.go]", "changed_files: [apps/control-plane/main.go]", 1)
		}, wantErr: true},
		{name: "forbidden path overrides allowed path", mutate: func(s string) string {
			s = strings.Replace(s, "forbidden: [apps/control-plane]", "forbidden: [tests/contracts]", 1)
			return strings.Replace(s, "changed_files: [tests/contracts/workflow/release_handoff.go]", "changed_files: [tests/contracts/workflow/release_handoff.go]", 1)
		}, wantErr: true},
		{name: "single component star scope passes", mutate: func(s string) string {
			return strings.Replace(s, "allowed: [tests/contracts/workflow, tests/contracts/cmd/luna-gate]", "allowed: [tests/contracts/workflow/*]", 1)
		}},
		{name: "single character scope passes", mutate: func(s string) string {
			return strings.Replace(s, "allowed: [tests/contracts/workflow, tests/contracts/cmd/luna-gate]", "allowed: ['tests/contracts/workflow/release_handoff.g?']", 1)
		}},
		{name: "recursive scope passes", mutate: func(s string) string {
			return strings.Replace(s, "allowed: [tests/contracts/workflow, tests/contracts/cmd/luna-gate]", "allowed: [tests/**]", 1)
		}},
		{name: "literal bracket filename passes", mutate: func(s string) string {
			s = strings.Replace(s, "allowed: [tests/contracts/workflow, tests/contracts/cmd/luna-gate]", "allowed: [apps/web]", 1)
			return strings.Replace(s, "changed_files: [tests/contracts/workflow/release_handoff.go]", "changed_files: ['apps/web/src/[id].tsx']", 1)
		}},
		{name: "traversal path rejected", mutate: func(s string) string {
			return strings.Replace(s, "changed_files: [tests/contracts/workflow/release_handoff.go]", "changed_files: [tests/contracts/../apps/main.go]", 1)
		}, wantErr: true},
		{name: "malformed scope glob rejected", mutate: func(s string) string {
			return strings.Replace(s, "allowed: [tests/contracts/workflow, tests/contracts/cmd/luna-gate]", "allowed: [tests/contracts/[]", 1)
		}, wantErr: true},
		{name: "missing finding evidence rejected", mutate: func(s string) string {
			return strings.Replace(s, "findings: []", "findings: [{severity: HIGH, status: RESOLVED, rationale: fixed}]", 1)
		}, wantErr: true},
		{name: "accepted finding missing actor rejected", mutate: func(s string) string {
			return strings.Replace(s, "findings: []", "findings: [{severity: LOW, status: ACCEPTED, rationale: accepted risk, evidence: [risk.md], accepted_by: [review-1]}]", 1)
		}, wantErr: true},
		{name: "high accepted finding rejected", mutate: func(s string) string {
			return strings.Replace(s, "findings: []", "findings: [{severity: HIGH, status: ACCEPTED, rationale: accepted risk, evidence: [risk.md], accepted_by: [review-1, release-1]}]", 1)
		}, wantErr: true},
		{name: "unknown field rejected", mutate: func(s string) string {
			return strings.Replace(s, "schema_version: 1", "schema_version: 1\nunknown: true", 1)
		}, wantErr: true},
		{name: "noncanonical readiness source rejected", mutate: func(s string) string { return strings.Replace(s, "source: luna-ba", "source: other", 1) }, wantErr: true},
		{name: "unresolved finding rejected", mutate: func(s string) string {
			return strings.Replace(s, "findings: []", "findings: [{severity: HIGH, status: OPEN, rationale: not fixed}]", 1)
		}, wantErr: true},
		{name: "duplicate key rejected", mutate: func(s string) string {
			return strings.Replace(s, "schema_version: 1", "schema_version: 1\nschema_version: 1", 1)
		}, wantErr: true},
		{name: "malformed yaml rejected", mutate: func(s string) string { return strings.Replace(s, "task:\n", "task\n", 1) }, wantErr: true},
		{name: "multiple documents rejected", mutate: func(s string) string { return s + "---\nschema_version: 1\n" }, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(strings.NewReader(tt.mutate(validHandoff)), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestRepositoryTemplateHasKnownFieldsButIsNotReleaseReady(t *testing.T) {
	path := "../../../.agents/task-handoff.yml"
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(contents)))
	decoder.KnownFields(true)
	var template handoff
	if err := decoder.Decode(&template); err != nil {
		t.Fatalf("repository template has an unknown or malformed field: %v", err)
	}
	if err := Validate(strings.NewReader(string(contents)), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"); err == nil || strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("template validation error = %v, want a non-unknown-field readiness failure", err)
	}
}
