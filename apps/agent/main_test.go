package main

import (
	"net/url"
	"strings"
	"testing"
)

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

func TestDockerLabelFilterEscapesLabelValues(t *testing.T) {
	encoded := dockerLabelFilter(`ark.platform.instance-id=map"with`, "ark.platform.node-id=node-1")
	decoded, err := url.QueryUnescape(encoded)
	if err != nil || !strings.Contains(decoded, `map\"with`) || !strings.Contains(decoded, "node-1") {
		t.Fatalf("label filter was not safely encoded: %q, %v", decoded, err)
	}
}

func TestLifecycleActionPathSupportsUpdateThroughRestart(t *testing.T) {
	for _, kind := range []string{"restart", "update"} {
		path, err := lifecycleActionPath(kind, "container-123")
		if err != nil || path != "/containers/container-123/restart?t=30" {
			t.Fatalf("%s path = %q, %v", kind, path, err)
		}
	}
}

func TestLifecycleActionPathRejectsUnsupportedActions(t *testing.T) {
	if _, err := lifecycleActionPath("backup", "container-123"); err == nil {
		t.Fatal("backup must remain unsupported until a safe archive worker exists")
	}
}
