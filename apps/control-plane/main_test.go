package main

import "testing"

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
