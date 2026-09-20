package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"strings"
	"syscall"
	"time"
)

type config struct{ publicURL, internalURL, nodeID, dockerHost, certFile, keyFile, caFile string }
type agent struct {
	cfg     config
	control *http.Client
	docker  *http.Client
}
type job struct {
	ID         string         `json:"id"`
	InstanceID string         `json:"instance_id"`
	Kind       string         `json:"kind"`
	Payload    map[string]any `json:"payload"`
}
type container struct {
	ID     string            `json:"Id"`
	State  string            `json:"State"`
	Labels map[string]string `json:"Labels"`
	Health *containerHealth  `json:"Health"`
}
type containerHealth struct {
	Status string `json:"Status"`
}
type observedInstance struct {
	InstanceID    string `json:"instance_id"`
	ContainerID   string `json:"container_id"`
	ObservedState string `json:"observed_state"`
	Health        string `json:"health"`
}

type desiredInstance struct {
	InstanceID   string `json:"instance_id"`
	DesiredState string `json:"desired_state"`
}

type heartbeatResponse struct {
	DesiredInstances []desiredInstance `json:"desired_instances"`
}

type backupResult struct {
	ID        string `json:"id"`
	ObjectKey string `json:"object_key"`
	Sha256    string `json:"sha256"`
	Bytes     int64  `json:"bytes"`
}

func main() {
	cfg := config{publicURL: getenv("CONTROL_PLANE_URL", "http://control-plane:8080"), internalURL: getenv("CONTROL_PLANE_INTERNAL_URL", "https://control-plane:8443"), nodeID: getenv("NODE_ID", "local-node"), dockerHost: dockerBaseURL(getenv("DOCKER_HOST", "tcp://socket-proxy:2375")), certFile: getenv("AGENT_TLS_CERT_FILE", "/run/ark-tls/agent.pem"), keyFile: getenv("AGENT_TLS_KEY_FILE", "/run/ark-tls/agent-key.pem"), caFile: getenv("AGENT_TLS_CA_FILE", "/run/ark-tls/ca.pem")}
	control, err := mtlsClient(cfg)
	if err != nil {
		log.Fatal(err)
	}
	docker := &http.Client{Timeout: 30 * time.Minute}
	a := &agent{cfg: cfg, control: control, docker: docker}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]string{"status": "ok", "node_id": cfg.nodeID})
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := a.publicHealth(r); err != nil {
			writeJSON(w, 503, map[string]string{"status": "not_ready", "error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]string{"status": "ready", "node_id": cfg.nodeID})
	})
	port := getenv("AGENT_PORT", "8090")
	server := &http.Server{Addr: ":" + port, Handler: mux}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go a.worker(ctx)
	log.Printf("agent %s listening on :%s", cfg.nodeID, port)
	serverErrors := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()
	select {
	case <-ctx.Done():
		log.Printf("agent %s shutting down", cfg.nodeID)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("agent shutdown: %v", err)
		}
	case err := <-serverErrors:
		log.Fatal(err)
	}
}

func (a *agent) worker(ctx context.Context) {
	heartbeat := time.NewTicker(10 * time.Second)
	poll := time.NewTicker(2 * time.Second)
	defer heartbeat.Stop()
	defer poll.Stop()
	_ = a.heartbeat()
	for {
		select {
		case <-heartbeat.C:
			_ = a.heartbeat()
		case <-poll.C:
			if err := a.poll(); err != nil {
				log.Printf("agent poll: %v", err)
			}
		case <-ctx.Done():
			return
		}
	}
}
func (a *agent) publicHealth(r *http.Request) error {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, a.cfg.publicURL+"/healthz", nil)
	if err != nil {
		return err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 500 {
		return fmt.Errorf("control plane status %d", res.StatusCode)
	}
	return nil
}
func (a *agent) heartbeat() error {
	var containers []container
	filter := dockerLabelFilter("ark.platform.node-id=" + a.cfg.nodeID)
	if err := a.dockerJSON(http.MethodGet, "/containers/json?all=true&filters="+filter, nil, &containers); err != nil {
		return fmt.Errorf("discover managed containers: %w", err)
	}
	instances := make([]observedInstance, 0, len(containers))
	for _, item := range containers {
		instanceID := item.Labels["ark.platform.instance-id"]
		if instanceID == "" {
			continue
		}
		health := "unknown"
		if item.Health != nil && item.Health.Status != "" {
			health = item.Health.Status
		}
		state := item.State
		if state == "exited" {
			state = "stopped"
		}
		instances = append(instances, observedInstance{InstanceID: instanceID, ContainerID: item.ID, ObservedState: state, Health: health})
	}
	body, err := json.Marshal(map[string]any{"instances": instances})
	if err != nil {
		return err
	}
	var response heartbeatResponse
	if err := a.controlJSON(http.MethodPost, "/internal/agent/heartbeat", bytes.NewReader(body), &response); err != nil {
		return err
	}
	byInstance := make(map[string]observedInstance, len(instances))
	for _, instance := range instances {
		byInstance[instance.InstanceID] = instance
	}
	for _, desired := range response.DesiredInstances {
		observed, ok := byInstance[desired.InstanceID]
		if !ok || observed.ContainerID == "" {
			continue
		}
		if action := desiredReconcileAction(desired.DesiredState, observed.ObservedState); action != "" {
			path, err := lifecycleActionPath(action, observed.ContainerID)
			if err != nil {
				return err
			}
			if err := a.dockerAction(http.MethodPost, path, nil); err != nil {
				return fmt.Errorf("reconcile instance %q: %w", desired.InstanceID, err)
			}
		}
	}
	return nil
}

func desiredReconcileAction(desiredState, observedState string) string {
	if desiredState == "running" && observedState != "running" && observedState != "unknown" {
		return "start"
	}
	if desiredState == "stopped" && observedState == "running" {
		return "stop"
	}
	return ""
}
func (a *agent) poll() error {
	var response job
	req, err := http.NewRequest(http.MethodPost, a.cfg.internalURL+"/internal/agent/lease", nil)
	if err != nil {
		return err
	}
	res, err := a.control.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNoContent {
		return nil
	}
	if res.StatusCode != 200 {
		return responseError(res)
	}
	if err = json.NewDecoder(res.Body).Decode(&response); err != nil {
		return err
	}
	result, err := a.execute(response)
	if err != nil {
		body, _ := json.Marshal(map[string]string{"error": err.Error()})
		_ = a.controlJSON(http.MethodPost, "/internal/agent/jobs/"+response.ID+"/fail", bytes.NewReader(body), &map[string]any{})
		return nil
	}
	var body io.Reader
	if result != nil {
		encoded, marshalErr := json.Marshal(map[string]any{"result": result})
		if marshalErr != nil {
			return marshalErr
		}
		body = bytes.NewReader(encoded)
	}
	return a.controlJSON(http.MethodPost, "/internal/agent/jobs/"+response.ID+"/complete", body, &map[string]any{})
}
func (a *agent) execute(j job) (any, error) {
	if j.InstanceID == "" {
		return nil, errors.New("job has no instance")
	}
	var items []container
	filter := dockerLabelFilter("ark.platform.instance-id="+j.InstanceID, "ark.platform.node-id="+a.cfg.nodeID)
	if err := a.dockerJSON(http.MethodGet, "/containers/json?all=true&filters="+filter, nil, &items); err != nil {
		return nil, fmt.Errorf("discover instance container: %w", err)
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("no managed container for instance %q", j.InstanceID)
	}
	id := items[0].ID
	if j.Kind == "backup" {
		result, err := a.createBackup(id, j.ID)
		return result, err
	}
	if j.Kind == "restore" {
		backupID, _ := j.Payload["backup_id"].(string)
		return nil, a.restoreBackup(id, backupID)
	}
	path, err := lifecycleActionPath(j.Kind, id)
	if err != nil {
		return nil, err
	}
	return nil, a.dockerAction(http.MethodPost, path, nil)
}

func lifecycleActionPath(kind, containerID string) (string, error) {
	switch kind {
	case "start":
		return "/containers/" + containerID + "/start", nil
	case "stop":
		return "/containers/" + containerID + "/stop?t=30", nil
	case "restart", "update":
		// The runtime's update-on-start hook performs the game update before
		// launching the server, so an update is a graceful restart.
		return "/containers/" + containerID + "/restart?t=30", nil
	default:
		return "", fmt.Errorf("action %q is not implemented by local agent", kind)
	}
}

func (a *agent) createBackup(containerID, jobID string) (backupResult, error) {
	response, err := a.dockerArchive(http.MethodGet, "/containers/"+containerID+"/archive?path="+url.QueryEscape("/opt/ark/data/save"), nil)
	if err != nil {
		return backupResult{}, err
	}
	defer response.Body.Close()
	compressed, err := os.CreateTemp("", "ark-backup-*.tar.gz")
	if err != nil {
		return backupResult{}, err
	}
	compressedName := compressed.Name()
	defer os.Remove(compressedName)
	digest := sha256.New()
	gz := gzip.NewWriter(io.MultiWriter(compressed, digest))
	if _, err = io.Copy(gz, response.Body); err != nil {
		compressed.Close()
		return backupResult{}, fmt.Errorf("read save archive: %w", err)
	}
	if err = gz.Close(); err != nil {
		compressed.Close()
		return backupResult{}, fmt.Errorf("finish save archive: %w", err)
	}
	if err = compressed.Close(); err != nil {
		return backupResult{}, err
	}
	stat, err := os.Stat(compressedName)
	if err != nil {
		return backupResult{}, err
	}
	backupID := fmt.Sprintf("ark-save-%s-%s.tar.gz", time.Now().UTC().Format("20060102T150405Z"), jobID)
	outer, err := os.CreateTemp("", "ark-backup-envelope-*.tar")
	if err != nil {
		return backupResult{}, err
	}
	outerName := outer.Name()
	defer os.Remove(outerName)
	if err = writeSingleFileTar(outer, backupID, compressedName, stat.Size()); err != nil {
		outer.Close()
		return backupResult{}, err
	}
	if err = outer.Close(); err != nil {
		return backupResult{}, err
	}
	if err = a.putArchive(containerID, "/opt/ark/data/backups", outerName); err != nil {
		return backupResult{}, fmt.Errorf("store backup: %w", err)
	}
	return backupResult{ID: backupID, ObjectKey: backupID, Sha256: hex.EncodeToString(digest.Sum(nil)), Bytes: stat.Size()}, nil
}

func (a *agent) restoreBackup(containerID, backupID string) error {
	if !validBackupName(backupID) {
		return errors.New("invalid backup name")
	}
	var inspect struct {
		State struct {
			Running bool `json:"Running"`
		} `json:"State"`
	}
	if err := a.dockerJSON(http.MethodGet, "/containers/"+containerID+"/json", nil, &inspect); err != nil {
		return fmt.Errorf("inspect instance before restore: %w", err)
	}
	if inspect.State.Running {
		return errors.New("refusing restore while the instance is running")
	}
	response, err := a.dockerArchive(http.MethodGet, "/containers/"+containerID+"/archive?path="+url.QueryEscape("/opt/ark/data/backups/"+backupID), nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	backupFile, err := os.CreateTemp("", "ark-restore-*.tar.gz")
	if err != nil {
		return err
	}
	backupFileName := backupFile.Name()
	defer os.Remove(backupFileName)
	if err = extractArchiveFile(response.Body, backupID, backupFile); err != nil {
		backupFile.Close()
		return fmt.Errorf("read backup archive: %w", err)
	}
	if err = backupFile.Close(); err != nil {
		return err
	}
	tarFile, err := os.CreateTemp("", "ark-restore-*.tar")
	if err != nil {
		return err
	}
	tarName := tarFile.Name()
	defer os.Remove(tarName)
	if err = repackBackupTar(backupFileName, tarFile); err != nil {
		tarFile.Close()
		return fmt.Errorf("validate backup contents: %w", err)
	}
	if err = tarFile.Close(); err != nil {
		return err
	}
	if err = a.putArchive(containerID, "/opt/ark/data/save", tarName); err != nil {
		return fmt.Errorf("restore save data: %w", err)
	}
	return nil
}

func (a *agent) dockerArchive(method, requestPath string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, a.cfg.dockerHost+requestPath, body)
	if err != nil {
		return nil, err
	}
	if method == http.MethodPut {
		req.Header.Set("Content-Type", "application/x-tar")
	}
	response, err := a.docker.Do(req)
	if err != nil {
		return nil, err
	}
	if response.StatusCode >= 300 {
		defer response.Body.Close()
		return nil, responseError(response)
	}
	return response, nil
}

func (a *agent) putArchive(containerID, target, fileName string) error {
	file, err := os.Open(fileName)
	if err != nil {
		return err
	}
	defer file.Close()
	response, err := a.dockerArchive(http.MethodPut, "/containers/"+containerID+"/archive?path="+url.QueryEscape(target), file)
	if err != nil {
		return err
	}
	response.Body.Close()
	return nil
}

func writeSingleFileTar(dst io.Writer, name, source string, size int64) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	writer := tar.NewWriter(dst)
	if err = writer.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: size}); err != nil {
		return err
	}
	if _, err = io.Copy(writer, input); err != nil {
		return err
	}
	return writer.Close()
}

func extractArchiveFile(source io.Reader, wanted string, dst io.Writer) error {
	reader := tar.NewReader(source)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		clean := path.Clean(header.Name)
		if path.IsAbs(header.Name) || clean == ".." || strings.HasPrefix(clean, "../") {
			return fmt.Errorf("unsafe archive entry %q", header.Name)
		}
		if path.Base(clean) != wanted || header.Typeflag != tar.TypeReg {
			continue
		}
		_, err = io.Copy(dst, reader)
		return err
	}
	return fmt.Errorf("backup %q not found in Docker archive", wanted)
}

func repackBackupTar(source string, dst io.Writer) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	gz, err := gzip.NewReader(input)
	if err != nil {
		return err
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	writer := tar.NewWriter(dst)
	for {
		header, nextErr := reader.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return nextErr
		}
		clean := path.Clean(header.Name)
		if clean == "." {
			continue
		}
		if path.IsAbs(header.Name) || clean == ".." || strings.HasPrefix(clean, "../") {
			return fmt.Errorf("unsafe archive path %q", header.Name)
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeDir {
			return fmt.Errorf("unsupported archive entry %q", header.Name)
		}
		header.Name = clean
		if err = writer.WriteHeader(header); err != nil {
			return err
		}
		if header.Typeflag == tar.TypeReg {
			if _, err = io.Copy(writer, reader); err != nil {
				return err
			}
		}
	}
	return writer.Close()
}

func validBackupName(value string) bool {
	return value != "" && path.Base(value) == value && strings.HasSuffix(value, ".tar.gz")
}

func dockerLabelFilter(labels ...string) string {
	payload, _ := json.Marshal(map[string][]string{"label": labels})
	return url.QueryEscape(string(payload))
}

func (a *agent) controlJSON(method, path string, body io.Reader, out any) error {
	req, err := http.NewRequest(method, a.cfg.internalURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := a.control.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return responseError(res)
	}
	if out != nil {
		return json.NewDecoder(res.Body).Decode(out)
	}
	return nil
}
func (a *agent) dockerJSON(method, path string, body io.Reader, out any) error {
	req, err := http.NewRequest(method, a.cfg.dockerHost+path, body)
	if err != nil {
		return err
	}
	res, err := a.docker.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return responseError(res)
	}
	if out != nil {
		return json.NewDecoder(res.Body).Decode(out)
	}
	return nil
}

func (a *agent) dockerAction(method, path string, body io.Reader) error {
	req, err := http.NewRequest(method, a.cfg.dockerHost+path, body)
	if err != nil {
		return err
	}
	res, err := a.docker.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotModified {
		return nil
	}
	if res.StatusCode >= 300 {
		return responseError(res)
	}
	return nil
}
func responseError(res *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
	return fmt.Errorf("status %s: %s", res.Status, string(body))
}
func mtlsClient(cfg config) (*http.Client, error) {
	cert, err := tls.LoadX509KeyPair(cfg.certFile, cfg.keyFile)
	if err != nil {
		return nil, fmt.Errorf("load agent mTLS certificate: %w", err)
	}
	raw, err := os.ReadFile(cfg.caFile)
	if err != nil {
		return nil, fmt.Errorf("read agent mTLS CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(raw) {
		return nil, errors.New("parse agent mTLS CA")
	}
	return &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{Certificates: []tls.Certificate{cert}, RootCAs: pool, MinVersion: tls.VersionTLS12}}}, nil
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
func dockerBaseURL(value string) string {
	if strings.HasPrefix(value, "tcp://") {
		return "http://" + strings.TrimPrefix(value, "tcp://")
	}
	return value
}
