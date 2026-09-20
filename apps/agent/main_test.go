package main

import "testing"

func TestDockerBaseURL(t *testing.T) {
	if got := dockerBaseURL("tcp://socket-proxy:2375"); got != "http://socket-proxy:2375" {
		t.Fatalf("tcp URL = %q", got)
	}
	if got := dockerBaseURL("http://docker:2375"); got != "http://docker:2375" {
		t.Fatalf("http URL = %q", got)
	}
	if got := dockerBaseURL("unix:///var/run/docker.sock"); got != "unix:///var/run/docker.sock" {
		t.Fatalf("unix URL = %q", got)
	}
}
