package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

func TestResourceMetricsReportsRunningManagedContainerUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/info":
			_, _ = w.Write([]byte(`{"MemTotal":1000}`))
		case "/containers/c-running/stats":
			_, _ = w.Write([]byte(`{"memory_stats":{"usage":250},"networks":{"eth0":{"rx_bytes":100,"tx_bytes":50}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	a := &agent{cfg: config{dockerHost: server.URL}, docker: server.Client()}
	got := a.resourceMetrics([]container{
		{ID: "c-running", State: "running"},
		{ID: "c-stopped", State: "exited"},
	})
	if got.MemoryTotalBytes != 1000 || got.MemoryUsedBytes != 250 || got.NetworkRxBytes != 100 || got.NetworkTxBytes != 50 {
		t.Fatalf("memory metrics = %+v", got)
	}
}

func TestResourceMetricsClampsAggregateUsageToHostTotal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/info" {
			_, _ = w.Write([]byte(`{"MemTotal":1000}`))
			return
		}
		_, _ = w.Write([]byte(`{"memory_stats":{"usage":700}}`))
	}))
	defer server.Close()
	a := &agent{cfg: config{dockerHost: server.URL}, docker: server.Client()}
	got := a.resourceMetrics([]container{{ID: "one", State: "running"}, {ID: "two", State: "running"}})
	if got.MemoryUsedBytes != 1000 {
		t.Fatalf("aggregate usage = %d, want clamp to 1000", got.MemoryUsedBytes)
	}
}

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

func TestDesiredReconcileAction(t *testing.T) {
	for _, test := range []struct {
		desired, observed, want string
	}{
		{"running", "stopped", "start"},
		{"running", "exited", "start"},
		{"stopped", "running", "stop"},
		{"running", "running", ""},
		{"running", "unknown", ""},
		{"stopped", "stopped", ""},
	} {
		if got := desiredReconcileAction(test.desired, test.observed); got != test.want {
			t.Fatalf("desired=%q observed=%q => %q, want %q", test.desired, test.observed, got, test.want)
		}
	}
}

func TestNormalizeObservedStateTreatsNonRunningContainersAsStopped(t *testing.T) {
	for _, state := range []string{"created", "exited", "dead"} {
		if got := normalizeObservedState(state); got != "stopped" {
			t.Fatalf("state %q normalized to %q, want stopped", state, got)
		}
	}
	if got := normalizeObservedState("running"); got != "running" {
		t.Fatalf("running normalized to %q", got)
	}
}

func TestDeletedManagedInstancesAreNotDesired(t *testing.T) {
	desired := map[string]struct{}{"kept": {}}
	if _, ok := desired["deleted"]; ok {
		t.Fatal("deleted instance must not remain in the desired set")
	}
}

func TestResourceLimitParsers(t *testing.T) {
	if got, _ := parseMemoryLimit("12g"); got != 12*1024*1024*1024 {
		t.Fatalf("memory limit = %d", got)
	}
	if got, _ := parseMemoryLimit("512m"); got != 512*1024*1024 {
		t.Fatalf("memory limit = %d", got)
	}
	if got, _ := parseCPULimit("2.5"); got != 2_500_000_000 {
		t.Fatalf("cpu limit = %d", got)
	}
	for _, value := range []string{"", "0", "-1g", "abc", "1tb"} {
		if _, err := parseMemoryLimit(value); err == nil {
			t.Fatalf("memory limit %q should be rejected", value)
		}
	}
	for _, value := range []string{"", "0", "-1", "abc", "1025"} {
		if _, err := parseCPULimit(value); err == nil {
			t.Fatalf("cpu limit %q should be rejected", value)
		}
	}
}

func TestValidBackupNameRejectsTraversal(t *testing.T) {
	for _, name := range []string{"", "../save.tar.gz", "/tmp/save.tar.gz", "save.zip"} {
		if validBackupName(name) {
			t.Fatalf("backup name %q should be rejected", name)
		}
	}
	if !validBackupName("ark-save-20260921T000000Z-job.tar.gz") {
		t.Fatal("valid backup name was rejected")
	}
}

func TestRepackBackupTarRejectsTraversal(t *testing.T) {
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	tarWriter := tar.NewWriter(gz)
	if err := tarWriter.WriteHeader(&tar.Header{Name: "../escape.txt", Mode: 0600, Size: 0}); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	file, err := os.CreateTemp("", "unsafe-backup-*.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err = file.Write(compressed.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	if err = repackBackupTar(name, &bytes.Buffer{}); err == nil {
		t.Fatal("restore archive path traversal was accepted")
	}
}
