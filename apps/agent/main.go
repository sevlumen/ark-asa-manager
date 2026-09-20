package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
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
}

func main() {
	cfg := config{publicURL: getenv("CONTROL_PLANE_URL", "http://control-plane:8080"), internalURL: getenv("CONTROL_PLANE_INTERNAL_URL", "https://control-plane:8443"), nodeID: getenv("NODE_ID", "local-node"), dockerHost: dockerBaseURL(getenv("DOCKER_HOST", "tcp://socket-proxy:2375")), certFile: getenv("AGENT_TLS_CERT_FILE", "/run/ark-tls/agent.pem"), keyFile: getenv("AGENT_TLS_KEY_FILE", "/run/ark-tls/agent-key.pem"), caFile: getenv("AGENT_TLS_CA_FILE", "/run/ark-tls/ca.pem")}
	control, err := mtlsClient(cfg)
	if err != nil {
		log.Fatal(err)
	}
	docker := &http.Client{Timeout: 20 * time.Second}
	a := &agent{cfg: cfg, control: control, docker: docker}
	go a.worker()
	http.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]string{"status": "ok", "node_id": cfg.nodeID})
	})
	http.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := a.publicHealth(r); err != nil {
			writeJSON(w, 503, map[string]string{"status": "not_ready", "error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]string{"status": "ready", "node_id": cfg.nodeID})
	})
	port := getenv("AGENT_PORT", "8090")
	log.Printf("agent %s listening on :%s", cfg.nodeID, port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func (a *agent) worker() {
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
	var out map[string]any
	return a.controlJSON(http.MethodPost, "/internal/agent/heartbeat", nil, &out)
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
	if err := a.execute(response); err != nil {
		body, _ := json.Marshal(map[string]string{"error": err.Error()})
		_ = a.controlJSON(http.MethodPost, "/internal/agent/jobs/"+response.ID+"/fail", bytes.NewReader(body), &map[string]any{})
		return nil
	}
	return a.controlJSON(http.MethodPost, "/internal/agent/jobs/"+response.ID+"/complete", nil, &map[string]any{})
}
func (a *agent) execute(j job) error {
	if j.InstanceID == "" {
		return errors.New("job has no instance")
	}
	var items []container
	filter := url.QueryEscape(fmt.Sprintf(`{"label":["ark.platform.instance-id=%s","ark.platform.node-id=%s"]}`, j.InstanceID, a.cfg.nodeID))
	if err := a.dockerJSON(http.MethodGet, "/containers/json?all=true&filters="+filter, nil, &items); err != nil {
		return fmt.Errorf("discover instance container: %w", err)
	}
	if len(items) == 0 {
		return fmt.Errorf("no managed container for instance %q", j.InstanceID)
	}
	id := items[0].ID
	switch j.Kind {
	case "start":
		return a.dockerAction(http.MethodPost, "/containers/"+id+"/start", nil)
	case "stop":
		return a.dockerAction(http.MethodPost, "/containers/"+id+"/stop?t=30", nil)
	case "restart":
		return a.dockerAction(http.MethodPost, "/containers/"+id+"/restart?t=30", nil)
	default:
		return fmt.Errorf("action %q is not implemented by local agent", j.Kind)
	}
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
