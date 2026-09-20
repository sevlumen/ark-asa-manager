package main

import (
	"strings"
	"testing"
	"time"
)

func TestPasswordHashRoundTrip(t *testing.T) {
	hash, err := hashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !verifyPassword("correct horse battery staple", hash) {
		t.Fatal("expected password to verify")
	}
	if verifyPassword("wrong password", hash) {
		t.Fatal("wrong password verified")
	}
}

func TestTokenHashIsDeterministicAndNotPlaintext(t *testing.T) {
	value := "session-secret"
	first := hashToken(value)
	if first == value || len(first) != 64 {
		t.Fatalf("unexpected token hash: %q", first)
	}
	if first != hashToken(value) {
		t.Fatal("token hash is not deterministic")
	}
}

func TestParseLimitBounds(t *testing.T) {
	for _, test := range []struct {
		input string
		want  int
	}{{"", 50}, {"0", 50}, {"101", 50}, {"25", 25}} {
		if got := parseLimit(test.input); got != test.want {
			t.Errorf("parseLimit(%q) = %d, want %d", test.input, got, test.want)
		}
	}
}

func TestCursorRoundTrip(t *testing.T) {
	cursor := encodeCursor("2026-09-20T19:00:54.499502Z", "job-1")
	parts, err := decodeCursor(cursor, 2)
	if err != nil || len(parts) != 2 || parts[0] != "2026-09-20T19:00:54.499502Z" || parts[1] != "job-1" {
		t.Fatalf("cursor round trip failed: %q, %v", parts, err)
	}
	if _, err := decodeCursor("%%%", 1); err == nil {
		t.Fatal("invalid cursor should fail")
	}
}

func TestLoginThrottleIsBoundedAndResettable(t *testing.T) {
	const ip, username = "test-ip", "test-user"
	clearLoginFailures(ip, username)
	for i := 0; i < loginThrottleMax; i++ {
		noteLoginFailure(ip, username)
	}
	if !loginBlocked(ip, username) {
		t.Fatal("login should be throttled after the limit")
	}
	clearLoginFailures(ip, username)
	if loginBlocked(ip, username) {
		t.Fatal("successful login reset should clear throttle state")
	}
}

func TestRoleCapabilities(t *testing.T) {
	viewer := roleCapabilities("viewer")
	admin := roleCapabilities("admin")
	for _, capability := range viewer {
		if capability == "write:instances" || capability == "manage:users" {
			t.Fatalf("viewer has write capability: %s", capability)
		}
	}
	found := false
	for _, capability := range admin {
		if capability == "manage:nodes" {
			found = true
		}
	}
	if !found {
		t.Fatal("admin is missing node management capability")
	}
}

func TestJobFailureStatus(t *testing.T) {
	for _, test := range []struct {
		status, want string
	}{
		{status: "queued", want: "job.requeued"},
		{status: "failed", want: "job.failed"},
	} {
		got := jobFailureEvent(test.status)
		if got != test.want {
			t.Errorf("status=%q: got %q, want %q", test.status, got, test.want)
		}
	}
}

func TestValidNodeEndpoint(t *testing.T) {
	valid := []string{"https://node.example:8443", "http://10.0.0.8:8080"}
	for _, value := range valid {
		if !validNodeEndpoint(value) {
			t.Errorf("validNodeEndpoint(%q) = false", value)
		}
	}
	invalid := []string{"node.example:8443", "ftp://node.example", "https://user:pass@node.example", "/var/run/node.sock", "https://"}
	for _, value := range invalid {
		if validNodeEndpoint(value) {
			t.Errorf("validNodeEndpoint(%q) = true", value)
		}
	}
}

func TestValidInstanceID(t *testing.T) {
	for _, value := range []string{"theisland", "cluster_1.eu", "ark-server-2"} {
		if !validInstanceID(value) {
			t.Errorf("validInstanceID(%q) = false", value)
		}
	}
	for _, value := range []string{"", ".hidden", "-leading", "has space", `quote"`, strings.Repeat("a", 64)} {
		if validInstanceID(value) {
			t.Errorf("validInstanceID(%q) = true", value)
		}
	}
}

func TestUserUpdateGuards(t *testing.T) {
	if !validRole("admin") || !validRole("operator") || !validRole("viewer") {
		t.Fatal("expected supported roles to validate")
	}
	if validRole("owner") {
		t.Fatal("unsupported role validated")
	}
}

func TestUserUpdateRequiresSeparateAdministratorForRoleChanges(t *testing.T) {
	// The handler enforces this invariant before any database mutation: an
	// administrator must use another active administrator to change roles.
	// Keep the rule explicit in the unit suite so a future refactor cannot
	// accidentally leave only the UI guard in place.
	if currentUserRoleChangeAllowed("admin-1", "admin-1", "viewer", "admin") {
		t.Fatal("current user role changes must be rejected")
	}
	if !currentUserRoleChangeAllowed("admin-1", "admin-2", "viewer", "admin") {
		t.Fatal("role changes for another user should remain allowed")
	}
}

func TestEffectiveNodeStatusMarksStaleHeartbeatOffline(t *testing.T) {
	now := time.Date(2026, 9, 21, 3, 0, 0, 0, time.UTC)
	fresh := now.Add(-heartbeatFreshness)
	stale := now.Add(-heartbeatFreshness - time.Second)
	if got := effectiveNodeStatus("online", &fresh, now); got != "online" {
		t.Fatalf("fresh heartbeat status = %q, want online", got)
	}
	if got := effectiveNodeStatus("online", &stale, now); got != "offline" {
		t.Fatalf("stale heartbeat status = %q, want offline", got)
	}
	if got := effectiveNodeStatus("unknown", nil, now); got != "unknown" {
		t.Fatalf("unknown node status = %q, want unknown", got)
	}
}

func TestOverallHealthStatusEscalatesQueryFailures(t *testing.T) {
	if got := overallHealthStatus("healthy", "healthy", false); got != "healthy" {
		t.Fatalf("healthy components = %q, want healthy", got)
	}
	if got := overallHealthStatus("degraded", "healthy", false); got != "degraded" {
		t.Fatalf("degraded agent = %q, want degraded", got)
	}
	if got := overallHealthStatus("healthy", "healthy", true); got != "critical" {
		t.Fatalf("failed health query = %q, want critical", got)
	}
}

func TestAgentLeaseQueryScopesJobsToOwningNode(t *testing.T) {
	if !strings.Contains(agentLeaseQuery, "JOIN instances i ON i.id=j.instance_id AND i.node_id=$1") {
		t.Fatal("agent lease query must restrict jobs to the leasing agent's node")
	}
	if !strings.Contains(agentLeaseQuery, "FOR UPDATE OF j SKIP LOCKED") {
		t.Fatal("agent lease query must retain row locking for concurrent agents")
	}
}

func TestAgentIdentityChecksEnrolledCertificateFingerprint(t *testing.T) {
	if !strings.Contains(agentIdentityQuery, "node_certificates") || !strings.Contains(agentIdentityQuery, "fingerprint") || !strings.Contains(agentIdentityQuery, "expires_at") {
		t.Fatal("agent identity must bind enrolled certificates to their recorded fingerprint")
	}
}

func TestBuildActionPayloadPreservesBackupID(t *testing.T) {
	payload, err := buildActionPayload("restore", "backup-123")
	if err != nil || payload["action"] != "restore" || payload["backup_id"] != "backup-123" {
		t.Fatalf("unexpected restore payload: %#v, %v", payload, err)
	}
	if _, err := buildActionPayload("restore", ""); err == nil {
		t.Fatal("restore without backup_id should be rejected")
	}
	backup, err := buildActionPayload("backup", "")
	if err != nil || backup["action"] != "backup" {
		t.Fatalf("backup payload should not require backup_id: %#v, %v", backup, err)
	}
}

func TestLifecycleActionConflicts(t *testing.T) {
	if !lifecycleActionConflicts("start", "running") {
		t.Fatal("start should conflict with a running instance")
	}
	if !lifecycleActionConflicts("stop", "stopped") {
		t.Fatal("stop should conflict with a stopped instance")
	}
	for _, test := range []struct{ action, observed string }{
		{"start", "stopped"}, {"start", "unknown"}, {"stop", "running"}, {"restart", "running"},
	} {
		if lifecycleActionConflicts(test.action, test.observed) {
			t.Errorf("%s with observed state %s should be allowed", test.action, test.observed)
		}
	}
}

func TestDesiredStateForAction(t *testing.T) {
	for _, test := range []struct {
		action, want string
	}{
		{"start", "running"},
		{"restart", "running"},
		{"stop", "stopped"},
	} {
		got, ok := desiredStateForAction(test.action)
		if !ok || got != test.want {
			t.Fatalf("desired state for %q = %q, %v; want %q, true", test.action, got, ok, test.want)
		}
	}
	if got, ok := desiredStateForAction("backup"); ok || got != "" {
		t.Fatalf("backup should not change desired state: %q, %v", got, ok)
	}
}

func TestValidBackupRecord(t *testing.T) {
	if !validBackupRecord("save.tar.gz", "save.tar.gz", strings.Repeat("a", 64), 1) {
		t.Fatal("valid backup record was rejected")
	}
	if validBackupRecord("save.tar.gz", "other.tar.gz", strings.Repeat("a", 64), 1) {
		t.Fatal("mismatched backup object key was accepted")
	}
	if validBackupRecord("save.tar.gz", "save.tar.gz", "not-a-sha", 1) {
		t.Fatal("invalid backup digest was accepted")
	}
}

func TestDecodeHeartbeatPayloadRejectsMalformedJSON(t *testing.T) {
	if _, err := decodeHeartbeatPayload(strings.NewReader("{not-json")); err == nil {
		t.Fatal("malformed heartbeat JSON should be rejected")
	}
	parsed, err := decodeHeartbeatPayload(strings.NewReader(`{"instances":[{"instance_id":"theisland","observed_state":"running"}]}`))
	if err != nil || len(parsed.Instances) != 1 || parsed.Instances[0].InstanceID != "theisland" {
		t.Fatalf("valid heartbeat JSON was not parsed: %#v, %v", parsed, err)
	}
}
