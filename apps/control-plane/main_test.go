package main

import "testing"

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
