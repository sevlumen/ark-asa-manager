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
	}{
		{"", 50}, {"0", 50}, {"101", 50}, {"25", 25},
	} {
		if got := parseLimit(test.input); got != test.want {
			t.Errorf("parseLimit(%q) = %d, want %d", test.input, got, test.want)
		}
	}
}
