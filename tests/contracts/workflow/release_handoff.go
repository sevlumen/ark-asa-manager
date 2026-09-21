// Package workflow validates the structural release handoff contract.
package workflow

import (
	"fmt"
	"io"
	pathpkg "path"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var shaPattern = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

type handoff struct {
	SchemaVersion  int            `yaml:"schema_version"`
	Task           task           `yaml:"task"`
	Requirements   readiness      `yaml:"requirements"`
	Plan           readiness      `yaml:"plan"`
	Scope          scope          `yaml:"scope"`
	Acceptance     []string       `yaml:"acceptance"`
	CurrentStage   string         `yaml:"current_stage"`
	HeadSHA        string         `yaml:"head_sha"`
	BaseSHA        string         `yaml:"base_sha"`
	Implementation implementation `yaml:"implementation"`
	Gates          gates          `yaml:"gates"`
	Security       *security      `yaml:"security"`
	Dependencies   dependencies   `yaml:"dependencies"`
	Owner          string         `yaml:"owner"`
	Branch         string         `yaml:"branch"`
	Worktree       string         `yaml:"worktree"`
	ChangedFiles   []string       `yaml:"changed_files"`
	Evidence       []string       `yaml:"evidence"`
	Blockers       []string       `yaml:"blockers"`
	Findings       []finding      `yaml:"findings"`
}

type task struct {
	Issue string `yaml:"issue"`
	Title string `yaml:"title"`
}

type readiness struct {
	Source   string   `yaml:"source"`
	Status   string   `yaml:"status"`
	ActorID  string   `yaml:"actor_id"`
	Evidence []string `yaml:"evidence"`
}

type scope struct {
	Allowed   []string `yaml:"allowed"`
	Forbidden []string `yaml:"forbidden"`
}

type implementation struct {
	Status   string   `yaml:"status"`
	ActorIDs []string `yaml:"actor_ids"`
	HeadSHA  string   `yaml:"head_sha"`
	Evidence []string `yaml:"evidence"`
}

type gate struct {
	Status   string   `yaml:"status"`
	ActorID  string   `yaml:"actor_id"`
	HeadSHA  string   `yaml:"head_sha"`
	BaseSHA  string   `yaml:"base_sha"`
	Evidence []string `yaml:"evidence"`
}

type gates struct {
	QC       gate `yaml:"qc"`
	Review   gate `yaml:"review"`
	Security gate `yaml:"security"`
	Release  gate `yaml:"release"`
}

type security struct {
	Required *bool    `yaml:"required"`
	Triggers []string `yaml:"triggers"`
	NAReason string   `yaml:"na_reason"`
}

type dependencies struct {
	Required []string `yaml:"required"`
	Blocks   []string `yaml:"blocks"`
}

type finding struct {
	Severity   string   `yaml:"severity"`
	Status     string   `yaml:"status"`
	Rationale  string   `yaml:"rationale"`
	Evidence   []string `yaml:"evidence"`
	AcceptedBy []string `yaml:"accepted_by"`
}

// Validate validates a release handoff against the expected commit pair.
// It performs structural and consistency checks only; it does not authenticate
// identities or contact GitHub.
func Validate(r io.Reader, expectedHead, expectedBase string) error {
	if r == nil {
		return fmt.Errorf("handoff reader is nil")
	}
	if !shaPattern.MatchString(expectedHead) || !shaPattern.MatchString(expectedBase) {
		return fmt.Errorf("expected head and base must be exact 40-hex SHAs")
	}

	decoder := yaml.NewDecoder(r)
	decoder.KnownFields(true)
	var h handoff
	if err := decoder.Decode(&h); err != nil {
		return fmt.Errorf("decode handoff: %w", err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("handoff contains multiple YAML documents")
		}
		return fmt.Errorf("decode trailing YAML document: %w", err)
	}

	if h.SchemaVersion != 1 {
		return fmt.Errorf("schema_version must be 1")
	}
	if !regexp.MustCompile(`^#[1-9][0-9]*$`).MatchString(strings.TrimSpace(h.Task.Issue)) {
		return fmt.Errorf("task.issue must be a positive issue number")
	}
	if blank(h.Task.Title) {
		return fmt.Errorf("task.title is required")
	}
	if h.Requirements.Source != "luna-ba" || h.Requirements.Status != "BA_READY" || blank(h.Requirements.ActorID) || emptyEvidence(h.Requirements.Evidence) {
		return fmt.Errorf("requirements must be BA_READY with actor and evidence")
	}
	if h.Plan.Source != "luna-planner" || h.Plan.Status != "PLAN_READY" || blank(h.Plan.ActorID) || emptyEvidence(h.Plan.Evidence) {
		return fmt.Errorf("plan must be PLAN_READY with actor and evidence")
	}
	if len(h.Scope.Allowed) == 0 || blankSlice(h.Scope.Allowed) || blankSlice(h.Scope.Forbidden) {
		return fmt.Errorf("scope.allowed must be non-empty and scope entries cannot be blank")
	}
	if emptyEvidence(h.Acceptance) {
		return fmt.Errorf("acceptance must be non-empty")
	}
	if h.CurrentStage != "RELEASE" {
		return fmt.Errorf("current_stage must be RELEASE")
	}
	if !matchingSHA(h.HeadSHA, expectedHead) || !matchingSHA(h.BaseSHA, expectedBase) {
		return fmt.Errorf("top-level head_sha/base_sha do not match expected SHAs")
	}
	if h.Implementation.Status != "IMPLEMENTED" || len(h.Implementation.ActorIDs) == 0 || blankSlice(h.Implementation.ActorIDs) || !unique(h.Implementation.ActorIDs) || !matchingSHA(h.Implementation.HeadSHA, expectedHead) || emptyEvidence(h.Implementation.Evidence) {
		return fmt.Errorf("implementation is incomplete or stale")
	}
	if h.Security == nil {
		return fmt.Errorf("security section is required")
	}
	if blank(h.Owner) || blank(h.Branch) || blank(h.Worktree) || len(h.ChangedFiles) == 0 || blankSlice(h.ChangedFiles) || emptyEvidence(h.Evidence) {
		return fmt.Errorf("owner, branch, worktree, changed_files, and evidence are required")
	}
	if err := validateScope(h.Scope, h.ChangedFiles); err != nil {
		return err
	}
	if len(h.Blockers) != 0 {
		return fmt.Errorf("blockers must be empty")
	}
	if err := validateSecurity(*h.Security, h.Gates.Security); err != nil {
		return err
	}

	implementationActors := make(map[string]bool, len(h.Implementation.ActorIDs))
	for _, actor := range h.Implementation.ActorIDs {
		implementationActors[strings.TrimSpace(actor)] = true
	}
	gateActors := map[string]bool{}
	for name, g := range map[string]gate{"qc": h.Gates.QC, "review": h.Gates.Review, "security": h.Gates.Security, "release": h.Gates.Release} {
		if err := validateGate(name, g, expectedHead, expectedBase, implementationActors, gateActors); err != nil {
			return err
		}
	}
	for i, f := range h.Findings {
		if err := validateFinding(i, f, h.Gates.Review.ActorID, h.Gates.Release.ActorID); err != nil {
			return err
		}
	}
	return nil
}

func validateGate(name string, g gate, head, base string, implementationActors, seen map[string]bool) error {
	validStatus := map[string]bool{"qc": g.Status == "QC_PASS", "review": g.Status == "REVIEW_PASS", "security": g.Status == "SECURITY_PASS" || g.Status == "N/A", "release": g.Status == "MERGE_READY"}
	if !validStatus[name] || blank(g.ActorID) || !matchingSHA(g.HeadSHA, head) || !matchingSHA(g.BaseSHA, base) || emptyEvidence(g.Evidence) {
		return fmt.Errorf("%s gate is incomplete, conditional, or stale", name)
	}
	actor := strings.TrimSpace(g.ActorID)
	if implementationActors[actor] {
		return fmt.Errorf("%s gate actor cannot review implementation", name)
	}
	if seen[actor] {
		return fmt.Errorf("gate actors must be distinct")
	}
	seen[actor] = true
	return nil
}

func validateSecurity(s security, g gate) error {
	if s.Required == nil {
		return fmt.Errorf("security.required must be true or false")
	}
	if len(s.Triggers) > 0 && !*s.Required {
		return fmt.Errorf("security triggers require security.required=true")
	}
	if blankSlice(s.Triggers) || (!*s.Required && (g.Status != "N/A" || blank(s.NAReason))) || (*s.Required && g.Status != "SECURITY_PASS") {
		return fmt.Errorf("security requirement and gate are inconsistent")
	}
	return nil
}

func validateFinding(index int, f finding, reviewActor, releaseActor string) error {
	if !map[string]bool{"BLOCKER": true, "HIGH": true, "MEDIUM": true, "LOW": true}[f.Severity] || !map[string]bool{"OPEN": true, "RESOLVED": true, "ACCEPTED": true}[f.Status] {
		return fmt.Errorf("finding %d has invalid severity or status", index)
	}
	if blank(f.Rationale) {
		return fmt.Errorf("finding %d requires rationale evidence", index)
	}
	if (f.Status == "RESOLVED" || f.Status == "ACCEPTED") && emptyEvidence(f.Evidence) {
		return fmt.Errorf("finding %d requires evidence", index)
	}
	if (f.Severity == "BLOCKER" || f.Severity == "HIGH") && f.Status != "RESOLVED" {
		return fmt.Errorf("finding %d must be resolved", index)
	}
	if (f.Severity == "MEDIUM" || f.Severity == "LOW") && f.Status == "OPEN" {
		return fmt.Errorf("finding %d requires resolution or acceptance", index)
	}
	if (f.Severity == "MEDIUM" || f.Severity == "LOW") && f.Status == "ACCEPTED" && blank(f.Rationale) {
		return fmt.Errorf("finding %d accepted status requires rationale", index)
	}
	if f.Status == "ACCEPTED" {
		accepted := make(map[string]bool, len(f.AcceptedBy))
		for _, actor := range f.AcceptedBy {
			actor = strings.TrimSpace(actor)
			if actor == "" || accepted[actor] {
				return fmt.Errorf("finding %d accepted_by must contain distinct actors", index)
			}
			accepted[actor] = true
		}
		if len(accepted) != 2 || !accepted[strings.TrimSpace(reviewActor)] || !accepted[strings.TrimSpace(releaseActor)] {
			return fmt.Errorf("finding %d accepted_by must be the review and release actors", index)
		}
	}
	return nil
}

func validateScope(s scope, changed []string) error {
	for _, pattern := range append(append([]string{}, s.Allowed...), s.Forbidden...) {
		if err := validatePath(pattern, true); err != nil {
			return fmt.Errorf("invalid scope pattern %q: %w", pattern, err)
		}
	}
	for _, file := range changed {
		if err := validatePath(file, false); err != nil {
			return fmt.Errorf("invalid changed file %q: %w", file, err)
		}
		if matchesAny(s.Forbidden, file) {
			return fmt.Errorf("changed file %q is forbidden", file)
		}
		if !matchesAny(s.Allowed, file) {
			return fmt.Errorf("changed file %q is outside allowed scope", file)
		}
	}
	return nil
}

func validatePath(value string, allowGlob bool) error {
	if value == "" || strings.TrimSpace(value) != value || strings.Contains(value, "\\") || strings.HasPrefix(value, "/") || (len(value) >= 2 && value[1] == ':') {
		return fmt.Errorf("path must be a repo-relative forward-slash path")
	}
	parts := strings.Split(value, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("path contains an empty or traversal component")
		}
		if !allowGlob && (strings.Contains(part, "*") || strings.Contains(part, "?")) {
			return fmt.Errorf("changed files cannot contain glob characters")
		}
		if allowGlob {
			if strings.Contains(part, "[") || strings.Contains(part, "]") || (strings.Contains(part, "**") && part != "**") {
				return fmt.Errorf("malformed glob")
			}
			if strings.ContainsAny(part, "*?") {
				if _, err := pathpkg.Match(part, "probe"); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func matchesAny(patterns []string, value string) bool {
	for _, pattern := range patterns {
		if matchPath(pattern, value) {
			return true
		}
	}
	return false
}

func matchPath(pattern, value string) bool {
	patternParts, valueParts := strings.Split(pattern, "/"), strings.Split(value, "/")
	if !strings.ContainsAny(pattern, "*?") {
		return value == pattern || strings.HasPrefix(value, pattern+"/")
	}
	return matchPathParts(patternParts, valueParts)
}

func matchPathParts(pattern, value []string) bool {
	if len(pattern) == 0 {
		return len(value) == 0
	}
	if pattern[0] == "**" {
		return matchPathParts(pattern[1:], value) || (len(value) > 0 && matchPathParts(pattern, value[1:]))
	}
	if len(value) == 0 {
		return false
	}
	matched, err := pathpkg.Match(pattern[0], value[0])
	return err == nil && matched && matchPathParts(pattern[1:], value[1:])
}

func matchingSHA(value, expected string) bool {
	return shaPattern.MatchString(value) && value == expected
}
func blank(value string) bool            { return strings.TrimSpace(value) == "" }
func emptyEvidence(values []string) bool { return len(values) == 0 || blankSlice(values) }
func blankSlice(values []string) bool {
	for _, value := range values {
		if blank(value) {
			return true
		}
	}
	return false
}
func unique(values []string) bool {
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if seen[value] {
			return false
		}
		seen[value] = true
	}
	return true
}
