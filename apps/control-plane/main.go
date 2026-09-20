package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/argon2"
)

const (
	sessionCookie = "ark_session"
	csrfCookie    = "ark_csrf"
	sessionTTL    = 12 * time.Hour
)

type contextKey string

const principalKey contextKey = "principal"

type principal struct {
	ID           string   `json:"id"`
	Username     string   `json:"username"`
	Role         string   `json:"role"`
	Capabilities []string `json:"capabilities"`
}
type server struct {
	db       *pgxpool.Pool
	secure   bool
	upgrader websocket.Upgrader
}

type loginThrottleEntry struct {
	Failures int
	ResetAt  time.Time
}

var loginThrottle = struct {
	sync.Mutex
	entries map[string]loginThrottleEntry
}{entries: make(map[string]loginThrottleEntry)}

const (
	loginThrottleWindow = time.Minute
	loginThrottleMax    = 5
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	healthcheck := flag.Bool("healthcheck", false, "check the local HTTP server and exit")
	flag.Parse()
	port := getenv("PORT", "8080")
	if *healthcheck {
		response, err := http.Get("http://127.0.0.1:" + port + "/healthz")
		if err != nil || response.StatusCode != 200 {
			if response != nil {
				response.Body.Close()
			}
			os.Exit(1)
		}
		response.Body.Close()
		return
	}
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is required")
	}
	db, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	s := &server{db: db, secure: strings.HasPrefix(strings.ToLower(os.Getenv("PUBLIC_ORIGIN")), "https://"), upgrader: websocket.Upgrader{CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		return origin == "" || origin == os.Getenv("PUBLIC_ORIGIN")
	}}}
	if err := s.bootstrapAdmin(ctx); err != nil {
		log.Fatal(err)
	}
	if err := s.reconcileLocalInstance(ctx); err != nil {
		log.Fatal(err)
	}
	internalServer, err := s.startInternalListener()
	if err != nil {
		log.Fatal(err)
	}
	r := chi.NewRouter()
	r.Use(s.requestID)
	r.Get("/healthz", s.health)
	r.Get("/readyz", s.ready)
	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/auth/login", s.login)
		r.Get("/auth/status", s.authStatus)
		r.Group(func(r chi.Router) {
			r.Use(s.requireSession)
			r.Post("/auth/logout", s.logout)
			r.Get("/auth/csrf", s.rotateCSRF)
			r.Get("/me", s.me)
			r.Get("/system/health", s.systemHealth)
			r.Get("/nodes", s.nodes)
			r.Get("/nodes/{id}", s.node)
			r.Get("/instances", s.instances)
			r.Get("/instances/{id}", s.instance)
			r.Patch("/instances/{id}", s.updateInstance)
			r.Post("/instances", s.createInstance)
			r.Post("/instances/{id}/actions/{action}", s.action)
			r.Get("/jobs", s.jobs)
			r.Get("/jobs/{id}", s.job)
			r.Get("/audit", s.audit)
			r.Get("/ws", s.websocket)
			r.Group(func(r chi.Router) {
				r.Use(s.requireRole("admin"))
				r.Post("/nodes", s.createNode)
				r.Get("/users", s.users)
				r.Post("/users", s.createUser)
				r.Patch("/users/{id}", s.updateUser)
			})
		})
	})
	publicServer := &http.Server{Addr: ":" + port, Handler: r}
	serverErrors := make(chan error, 2)
	go func() {
		log.Printf("control-plane listening on :%s", port)
		if err := publicServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()
	select {
	case <-ctx.Done():
		log.Print("control-plane shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := publicServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("control-plane shutdown: %v", err)
		}
		if internalServer != nil {
			if err := internalServer.Shutdown(shutdownCtx); err != nil {
				log.Printf("private agent listener shutdown: %v", err)
			}
		}
	case err := <-serverErrors:
		log.Fatal(err)
	}
}

func (s *server) startInternalListener() (*http.Server, error) {
	certFile, keyFile, caFile := os.Getenv("INTERNAL_TLS_CERT_FILE"), os.Getenv("INTERNAL_TLS_KEY_FILE"), os.Getenv("INTERNAL_TLS_CA_FILE")
	if certFile == "" || keyFile == "" || caFile == "" {
		log.Print("internal agent listener disabled: TLS files are not configured")
		return nil, nil
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load internal TLS certificate: %w", err)
	}
	caBytes, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read internal TLS CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caBytes) {
		return nil, errors.New("parse internal TLS CA")
	}
	internal := chi.NewRouter()
	internal.Post("/internal/agent/lease", s.agentLease)
	internal.Post("/internal/agent/jobs/{id}/complete", s.agentComplete)
	internal.Post("/internal/agent/jobs/{id}/fail", s.agentFail)
	internal.Post("/internal/agent/heartbeat", s.agentHeartbeat)
	listener, err := net.Listen("tcp", ":"+getenv("INTERNAL_PORT", "8443"))
	if err != nil {
		return nil, err
	}
	tlsListener := tls.NewListener(listener, &tls.Config{Certificates: []tls.Certificate{cert}, ClientCAs: pool, ClientAuth: tls.RequireAndVerifyClientCert, MinVersion: tls.VersionTLS12})
	internalServer := &http.Server{Handler: internal}
	go func() {
		log.Printf("private agent listener listening on :%s", getenv("INTERNAL_PORT", "8443"))
		if err := internalServer.Serve(tlsListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("private agent listener stopped: %v", err)
		}
	}()
	return internalServer, nil
}

func (s *server) bootstrapAdmin(ctx context.Context) error {
	var count int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return nil
	}
	username := strings.TrimSpace(os.Getenv("ADMIN_USERNAME"))
	path := os.Getenv("ADMIN_BOOTSTRAP_PASSWORD_FILE")
	if username == "" || path == "" {
		log.Print("admin bootstrap skipped: ADMIN_USERNAME and ADMIN_BOOTSTRAP_PASSWORD_FILE are required for an empty database")
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read admin bootstrap secret: %w", err)
	}
	password := strings.TrimSpace(string(raw))
	if len(password) < 12 {
		return errors.New("admin bootstrap password must be at least 12 characters")
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	id, err := randomID()
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `INSERT INTO users (id, username, password_hash, role) VALUES ($1, $2, $3, 'admin') ON CONFLICT (username) DO NOTHING`, id, username, hash)
	return err
}

func (s *server) reconcileLocalInstance(ctx context.Context) error {
	if strings.EqualFold(os.Getenv("BOOTSTRAP_LOCAL_INSTANCE"), "false") {
		return nil
	}
	nodeID := getenv("NODE_ID", "local-node")
	instanceID := getenv("ARK_INSTANCE_ID", "theisland")
	mapName := getenv("ARK_MAP", "TheIsland_WP")
	clusterID := getenv("ARK_CLUSTER_ID", "local-cluster")
	if _, err := s.db.Exec(ctx, `INSERT INTO nodes (id,name,endpoint,status) VALUES ($1,$2,$3,'unknown') ON CONFLICT (id) DO UPDATE SET name=EXCLUDED.name, endpoint=EXCLUDED.endpoint, updated_at=now()`, nodeID, nodeID, "agent:"+nodeID); err != nil {
		return fmt.Errorf("reconcile local node: %w", err)
	}
	if _, err := s.db.Exec(ctx, `INSERT INTO instances (id,node_id,map_name,cluster_id,desired_state) VALUES ($1,$2,$3,$4,'stopped') ON CONFLICT (id) DO UPDATE SET node_id=EXCLUDED.node_id, map_name=EXCLUDED.map_name, cluster_id=EXCLUDED.cluster_id, updated_at=now()`, instanceID, nodeID, mapName, clusterID); err != nil {
		return fmt.Errorf("reconcile local instance: %w", err)
	}
	_, err := s.db.Exec(ctx, `INSERT INTO instance_status (instance_id,observed_state,health) VALUES ($1,'unknown','unknown') ON CONFLICT (instance_id) DO NOTHING`, instanceID)
	if err != nil {
		return fmt.Errorf("reconcile local instance status: %w", err)
	}
	return nil
}

func (s *server) agentIdentity(r *http.Request) (string, bool) {
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		return "", false
	}
	nodeID := r.TLS.PeerCertificates[0].Subject.CommonName
	var exists bool
	if err := s.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM nodes WHERE id=$1)`, nodeID).Scan(&exists); err != nil || !exists {
		return "", false
	}
	return nodeID, true
}

func (s *server) agentLease(w http.ResponseWriter, r *http.Request) {
	nodeID, ok := s.agentIdentity(r)
	if !ok {
		writeError(w, http.StatusForbidden, "forbidden", "unknown agent identity")
		return
	}
	var id string
	var instanceID, kind string
	var payload []byte
	var attempts int
	var expires time.Time
	err := s.db.QueryRow(r.Context(), `UPDATE jobs SET status='leased', lease_owner=$1, lease_expires_at=now()+interval '60 seconds', attempts=attempts+1, started_at=COALESCE(started_at,now()) WHERE id=(SELECT id FROM jobs WHERE (status='queued' OR (status IN ('leased','running') AND lease_expires_at < now())) AND attempts < max_attempts ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING id,instance_id,kind,payload,attempts,lease_expires_at`, nodeID).Scan(&id, &instanceID, &kind, &payload, &attempts, &expires)
	if errors.Is(err, pgx.ErrNoRows) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		writeError(w, 500, "internal_error", "could not lease job")
		return
	}
	var decoded any
	_ = json.Unmarshal(payload, &decoded)
	writeJSON(w, 200, map[string]any{"id": id, "instance_id": instanceID, "kind": kind, "payload": decoded, "attempts": attempts, "lease_expires_at": expires, "owner": nodeID})
}

func (s *server) agentComplete(w http.ResponseWriter, r *http.Request) {
	nodeID, ok := s.agentIdentity(r)
	if !ok {
		writeError(w, 403, "forbidden", "unknown agent identity")
		return
	}
	id := chi.URLParam(r, "id")
	result, err := s.db.Exec(r.Context(), `UPDATE jobs SET status='succeeded',lease_owner=NULL,lease_expires_at=NULL,finished_at=now(),last_error=NULL WHERE id=$1 AND lease_owner=$2`, id, nodeID)
	if err != nil {
		writeError(w, 500, "internal_error", "could not complete job")
		return
	}
	if result.RowsAffected() != 1 {
		writeError(w, 409, "conflict", "job lease is no longer owned by this agent")
		return
	}
	s.appendEvent(r.Context(), "job.succeeded", "job", id, map[string]any{"owner": nodeID})
	writeJSON(w, 200, map[string]string{"status": "succeeded", "id": id})
}

func (s *server) agentFail(w http.ResponseWriter, r *http.Request) {
	nodeID, ok := s.agentIdentity(r)
	if !ok {
		writeError(w, 403, "forbidden", "unknown agent identity")
		return
	}
	id := chi.URLParam(r, "id")
	var input struct {
		Error string `json:"error"`
	}
	_ = decodeErrorBody(r, &input)
	var status string
	err := s.db.QueryRow(r.Context(), `UPDATE jobs SET status=CASE WHEN attempts>=max_attempts THEN 'failed' ELSE 'queued' END,lease_owner=NULL,lease_expires_at=NULL,last_error=$3,finished_at=CASE WHEN attempts>=max_attempts THEN now() ELSE NULL END WHERE id=$1 AND lease_owner=$2 RETURNING status`, id, nodeID, input.Error).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 409, "conflict", "job lease is no longer owned by this agent")
		return
	}
	if err != nil {
		writeError(w, 500, "internal_error", "could not fail job")
		return
	}
	eventType := jobFailureEvent(status)
	s.appendEvent(r.Context(), eventType, "job", id, map[string]any{"owner": nodeID, "error": input.Error, "status": status})
	writeJSON(w, 200, map[string]string{"status": status, "id": id})
}

func jobFailureEvent(status string) string {
	if status == "queued" {
		return "job.requeued"
	}
	return "job.failed"
}

func (s *server) agentHeartbeat(w http.ResponseWriter, r *http.Request) {
	nodeID, ok := s.agentIdentity(r)
	if !ok {
		writeError(w, 403, "forbidden", "unknown agent identity")
		return
	}
	input, err := decodeHeartbeatPayload(r.Body)
	if err != nil {
		writeError(w, 400, "invalid_request", "invalid heartbeat payload")
		return
	}
	result, err := s.db.Exec(r.Context(), `UPDATE nodes SET status='online',last_heartbeat=now(),updated_at=now() WHERE id=$1`, nodeID)
	if err != nil {
		writeError(w, 500, "internal_error", "could not update heartbeat")
		return
	}
	if result.RowsAffected() != 1 {
		writeError(w, 404, "not_found", "node not found")
		return
	}
	observed := make(map[string]struct {
		containerID, state, health string
	})
	for _, instance := range input.Instances {
		if instance.InstanceID == "" {
			continue
		}
		if instance.ObservedState == "" {
			instance.ObservedState = "unknown"
		}
		if instance.Health == "" {
			instance.Health = "unknown"
		}
		observed[instance.InstanceID] = struct{ containerID, state, health string }{instance.ContainerID, instance.ObservedState, instance.Health}
	}
	rows, err := s.db.Query(r.Context(), `SELECT i.id,COALESCE(st.observed_state,'unknown'),COALESCE(st.health,'unknown'),st.last_error FROM instances i LEFT JOIN instance_status st ON st.instance_id=i.id WHERE i.node_id=$1`, nodeID)
	if err != nil {
		writeError(w, 500, "internal_error", "could not read instance heartbeat state")
		return
	}
	type previousStatus struct {
		id, state, health string
		lastError         *string
	}
	previous := make([]previousStatus, 0)
	for rows.Next() {
		var item previousStatus
		if err := rows.Scan(&item.id, &item.state, &item.health, &item.lastError); err != nil {
			rows.Close()
			writeError(w, 500, "internal_error", "could not read instance heartbeat state")
			return
		}
		previous = append(previous, item)
	}
	rows.Close()
	for _, item := range previous {
		current, ok := observed[item.id]
		state, health, containerID := "unknown", "unknown", ""
		var lastError *string
		if ok {
			state, health, containerID = current.state, current.health, current.containerID
		} else {
			missing := "managed container not found"
			lastError = &missing
		}
		changed := item.state != state || item.health != health || (item.lastError == nil) != (lastError == nil)
		_, err := s.db.Exec(r.Context(), `INSERT INTO instance_status (instance_id,observed_state,container_id,health,last_error,observed_at) VALUES ($1,$2,$3,$4,$5,now()) ON CONFLICT (instance_id) DO UPDATE SET observed_state=EXCLUDED.observed_state,container_id=EXCLUDED.container_id,health=EXCLUDED.health,last_error=EXCLUDED.last_error,observed_at=now()`, item.id, state, containerID, health, lastError)
		if err != nil {
			writeError(w, 500, "internal_error", "could not update instance heartbeat")
			return
		}
		if changed {
			s.appendEvent(r.Context(), "instance.status_changed", "instance", item.id, map[string]any{"observed_state": state, "health": health, "last_error": lastError})
		}
	}
	writeJSON(w, 200, map[string]any{"node_id": nodeID, "status": "online", "observed_at": time.Now().UTC()})
}

func decodeHeartbeatPayload(reader io.Reader) (struct {
	Instances []struct {
		InstanceID    string `json:"instance_id"`
		ContainerID   string `json:"container_id"`
		ObservedState string `json:"observed_state"`
		Health        string `json:"health"`
	} `json:"instances"`
}, error) {
	var input struct {
		Instances []struct {
			InstanceID    string `json:"instance_id"`
			ContainerID   string `json:"container_id"`
			ObservedState string `json:"observed_state"`
			Health        string `json:"health"`
		} `json:"instances"`
	}
	if err := json.NewDecoder(reader).Decode(&input); err != nil && !errors.Is(err, io.EOF) {
		return input, err
	}
	return input, nil
}

func decodeErrorBody(r *http.Request, dst any) error {
	if r.Body == nil {
		return nil
	}
	return json.NewDecoder(r.Body).Decode(dst)
}
func (s *server) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			id, _ = randomID()
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r)
	})
}
func (s *server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]string{"status": "ok"})
}
func (s *server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.db.Ping(ctx); err != nil {
		writeError(w, 503, "not_ready", "database unavailable")
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ready"})
}
func (s *server) systemHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.db.Ping(ctx); err != nil {
		writeJSON(w, 200, map[string]any{"status": "critical", "control_plane": "critical", "database": "critical", "agent": "unknown", "ark": "unknown", "observed_at": time.Now().UTC()})
		return
	}
	agentStatus := "unknown"
	var online int
	_ = s.db.QueryRow(ctx, `SELECT count(*) FROM nodes WHERE status='online' AND last_heartbeat > now() - interval '30 seconds'`).Scan(&online)
	if online > 0 {
		agentStatus = "healthy"
	} else {
		var nodes int
		_ = s.db.QueryRow(ctx, `SELECT count(*) FROM nodes`).Scan(&nodes)
		if nodes > 0 {
			agentStatus = "degraded"
		}
	}
	arkStatus := "unknown"
	var instances, unhealthy int
	if err := s.db.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE COALESCE(st.health,'unknown') <> 'healthy') FROM instances i LEFT JOIN instance_status st ON st.instance_id=i.id`).Scan(&instances, &unhealthy); err == nil && instances > 0 {
		arkStatus = "healthy"
		if unhealthy > 0 {
			arkStatus = "degraded"
		}
	}
	status := "healthy"
	if agentStatus != "healthy" || (instances > 0 && arkStatus != "healthy") {
		status = "degraded"
	}
	writeJSON(w, 200, map[string]any{"status": status, "control_plane": "healthy", "database": "healthy", "agent": agentStatus, "ark": arkStatus, "observed_at": time.Now().UTC()})
}

func requestIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	if r.RemoteAddr != "" {
		return r.RemoteAddr
	}
	return "unknown"
}

func loginKeys(ip, username string) []string {
	return []string{"ip:" + ip, "username:" + strings.ToLower(strings.TrimSpace(username))}
}

func loginBlocked(ip, username string) bool {
	now := time.Now()
	loginThrottle.Lock()
	defer loginThrottle.Unlock()
	blocked := false
	for _, key := range loginKeys(ip, username) {
		entry, ok := loginThrottle.entries[key]
		if !ok || now.After(entry.ResetAt) {
			continue
		}
		if entry.Failures >= loginThrottleMax {
			blocked = true
		}
	}
	return blocked
}

func noteLoginFailure(ip, username string) {
	now := time.Now()
	loginThrottle.Lock()
	defer loginThrottle.Unlock()
	for key, entry := range loginThrottle.entries {
		if now.After(entry.ResetAt) {
			delete(loginThrottle.entries, key)
		}
	}
	for _, key := range loginKeys(ip, username) {
		entry := loginThrottle.entries[key]
		if now.After(entry.ResetAt) {
			entry = loginThrottleEntry{ResetAt: now.Add(loginThrottleWindow)}
		}
		entry.Failures++
		loginThrottle.entries[key] = entry
	}
}

func clearLoginFailures(ip, username string) {
	loginThrottle.Lock()
	defer loginThrottle.Unlock()
	for _, key := range loginKeys(ip, username) {
		delete(loginThrottle.entries, key)
	}
}

func (s *server) nodes(w http.ResponseWriter, r *http.Request) {
	limit := parseLimit(r.URL.Query().Get("limit"))
	parts, err := decodeCursor(r.URL.Query().Get("cursor"), 2)
	if cursorError(w, err) {
		return
	}
	query := `SELECT id,name,endpoint,status,last_heartbeat,created_at FROM nodes`
	args := []any{limit + 1}
	if len(parts) == 2 {
		query += ` WHERE (name,id) > ($2,$3)`
		args = append(args, parts[0], parts[1])
	}
	query += ` ORDER BY name,id LIMIT $1`
	rows, err := s.db.Query(r.Context(), query, args...)
	if err != nil {
		writeError(w, 500, "internal_error", "could not list nodes")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0, limit+1)
	for rows.Next() {
		var id, name, endpoint, status string
		var heartbeat, created *time.Time
		if err := rows.Scan(&id, &name, &endpoint, &status, &heartbeat, &created); err != nil {
			writeError(w, 500, "internal_error", "could not read nodes")
			return
		}
		items = append(items, map[string]any{"id": id, "name": name, "endpoint": endpoint, "status": effectiveNodeStatus(status, heartbeat, time.Now()), "last_heartbeat": heartbeat, "created_at": created})
	}
	next := ""
	if len(items) > limit {
		next = encodeCursor(items[limit-1]["name"].(string), items[limit-1]["id"].(string))
		items = items[:limit]
	}
	writeJSON(w, 200, map[string]any{"items": items, "count": len(items), "next_cursor": next})
}

func (s *server) node(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var name, endpoint, status string
	var heartbeat, created *time.Time
	err := s.db.QueryRow(r.Context(), `SELECT name,endpoint,status,last_heartbeat,created_at FROM nodes WHERE id=$1`, id).Scan(&name, &endpoint, &status, &heartbeat, &created)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found", "node not found")
		return
	}
	if err != nil {
		writeError(w, 500, "internal_error", "could not read node")
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "name": name, "endpoint": endpoint, "status": effectiveNodeStatus(status, heartbeat, time.Now()), "last_heartbeat": heartbeat, "created_at": created})
}

func (s *server) createNode(w http.ResponseWriter, r *http.Request) {
	var input struct{ ID, Name, Endpoint string }
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Endpoint = strings.TrimSpace(input.Endpoint)
	if input.Name == "" || !validNodeEndpoint(input.Endpoint) {
		writeError(w, 400, "invalid_request", "name and endpoint are required")
		return
	}
	id := strings.TrimSpace(input.ID)
	if id == "" {
		id, _ = randomID()
	}
	if _, err := s.db.Exec(r.Context(), `INSERT INTO nodes (id,name,endpoint) VALUES ($1,$2,$3)`, id, input.Name, input.Endpoint); err != nil {
		writeError(w, 409, "conflict", "node already exists")
		return
	}
	p := currentPrincipal(r)
	s.recordAudit(r.Context(), p.Username, "node.create", "node:"+id, map[string]any{"outcome": "allowed"})
	writeJSON(w, 201, map[string]any{"id": id, "name": input.Name, "endpoint": input.Endpoint, "status": "unknown", "last_heartbeat": nil})
}

func validNodeEndpoint(value string) bool {
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

func validRole(value string) bool {
	return value == "admin" || value == "operator" || value == "viewer"
}

func currentUserRoleChangeAllowed(targetID, principalID, requestedRole, currentRole string) bool {
	return targetID != principalID || requestedRole == currentRole
}

const heartbeatFreshness = 30 * time.Second

func effectiveNodeStatus(status string, lastHeartbeat *time.Time, now time.Time) string {
	if status == "online" && (lastHeartbeat == nil || now.Sub(*lastHeartbeat) > heartbeatFreshness) {
		return "offline"
	}
	return status
}

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	var input struct{ Username, Password string }
	if !decodeJSON(w, r, &input) {
		noteLoginFailure(requestIP(r), "")
		return
	}
	if input.Username == "" || input.Password == "" {
		noteLoginFailure(requestIP(r), input.Username)
		writeError(w, 401, "unauthorized", "invalid credentials")
		return
	}
	if loginBlocked(requestIP(r), input.Username) {
		s.recordAudit(r.Context(), "anonymous", "auth.login_throttled", "user:"+input.Username, map[string]any{"outcome": "denied"})
		writeError(w, 401, "unauthorized", "invalid credentials")
		return
	}
	var p principal
	var passwordHash string
	var disabled *time.Time
	err := s.db.QueryRow(r.Context(), `SELECT id, username, password_hash, role, disabled_at FROM users WHERE username=$1`, input.Username).Scan(&p.ID, &p.Username, &passwordHash, &p.Role, &disabled)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 500, "internal_error", "authentication unavailable")
		return
	}
	if err != nil || disabled != nil || !verifyPassword(input.Password, passwordHash) {
		noteLoginFailure(requestIP(r), input.Username)
		s.recordAudit(r.Context(), "anonymous", "auth.login_failed", "user:"+input.Username, map[string]any{"outcome": "denied"})
		writeError(w, 401, "unauthorized", "invalid credentials")
		return
	}
	clearLoginFailures(requestIP(r), input.Username)
	p.Capabilities = roleCapabilities(p.Role)
	sessionID, err := randomID()
	if err != nil {
		writeError(w, 500, "internal_error", "could not create session")
		return
	}
	csrf, err := randomID()
	if err != nil {
		writeError(w, 500, "internal_error", "could not create csrf token")
		return
	}
	expires := time.Now().Add(sessionTTL)
	if _, err = s.db.Exec(r.Context(), `INSERT INTO sessions (id,user_id,csrf_hash,expires_at) VALUES ($1,$2,$3,$4)`, sessionID, p.ID, hashToken(csrf), expires); err != nil {
		writeError(w, 500, "internal_error", "could not create session")
		return
	}
	s.setCookie(w, sessionCookie, sessionID, true, expires)
	s.setCookie(w, csrfCookie, csrf, false, expires)
	s.recordAudit(r.Context(), p.Username, "auth.login", "session:"+sessionID, map[string]any{"outcome": "allowed"})
	writeJSON(w, 200, map[string]any{"user": p, "expires_at": expires})
}
func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	p := currentPrincipal(r)
	if c, err := r.Cookie(sessionCookie); err == nil {
		_, _ = s.db.Exec(r.Context(), `UPDATE sessions SET revoked_at=now() WHERE id=$1`, c.Value)
		s.recordAudit(r.Context(), p.Username, "auth.logout", "session:"+c.Value, map[string]any{"outcome": "allowed"})
	}
	s.setCookie(w, sessionCookie, "", true, time.Unix(0, 0))
	s.setCookie(w, csrfCookie, "", false, time.Unix(0, 0))
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func (s *server) rotateCSRF(w http.ResponseWriter, r *http.Request) {
	session, err := r.Cookie(sessionCookie)
	if err != nil || session.Value == "" {
		writeError(w, 401, "unauthorized", "authentication required")
		return
	}
	token, err := randomID()
	if err != nil {
		writeError(w, 500, "internal_error", "could not create csrf token")
		return
	}
	result, err := s.db.Exec(r.Context(), `UPDATE sessions SET csrf_hash=$2 WHERE id=$1 AND revoked_at IS NULL AND expires_at > now()`, session.Value, hashToken(token))
	if err != nil {
		writeError(w, 500, "internal_error", "could not rotate csrf token")
		return
	}
	if result.RowsAffected() != 1 {
		writeError(w, 401, "unauthorized", "authentication required")
		return
	}
	s.setCookie(w, csrfCookie, token, false, time.Now().Add(sessionTTL))
	writeJSON(w, 200, map[string]string{"status": "ok"})
}
func (s *server) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"user": currentPrincipal(r)})
}

func (s *server) authStatus(w http.ResponseWriter, r *http.Request) {
	if p, ok := s.sessionPrincipal(r); ok {
		writeJSON(w, 200, map[string]any{"authenticated": true, "user": p})
		return
	}
	writeJSON(w, 200, map[string]any{"authenticated": false})
}

func (s *server) instances(w http.ResponseWriter, r *http.Request) {
	limit := parseLimit(r.URL.Query().Get("limit"))
	parts, err := decodeCursor(r.URL.Query().Get("cursor"), 1)
	if cursorError(w, err) {
		return
	}
	query := `SELECT i.id,i.node_id,i.map_name,i.cluster_id,i.desired_state,COALESCE(st.observed_state,'unknown'),COALESCE(st.health,'unknown'),st.last_error,COALESCE(st.observed_at,i.updated_at) FROM instances i LEFT JOIN instance_status st ON st.instance_id=i.id`
	args := []any{limit + 1}
	if len(parts) == 1 {
		query += ` WHERE i.id > $2`
		args = append(args, parts[0])
	}
	query += ` ORDER BY i.id LIMIT $1`
	rows, err := s.db.Query(r.Context(), query, args...)
	if err != nil {
		writeError(w, 500, "internal_error", "could not list instances")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0, limit+1)
	for rows.Next() {
		var id, node, mapName, cluster, desired, observed, health string
		var lastError *string
		var observedAt time.Time
		if err := rows.Scan(&id, &node, &mapName, &cluster, &desired, &observed, &health, &lastError, &observedAt); err != nil {
			writeError(w, 500, "internal_error", "could not read instances")
			return
		}
		items = append(items, map[string]any{"id": id, "node_id": node, "map": mapName, "map_name": mapName, "cluster_id": cluster, "desired_state": desired, "observed_state": observed, "health": health, "last_error": lastError, "observed_at": observedAt})
	}
	next := ""
	if len(items) > limit {
		next = encodeCursor(items[limit-1]["id"].(string))
		items = items[:limit]
	}
	writeJSON(w, 200, map[string]any{"items": items, "count": len(items), "next_cursor": next})
}

func (s *server) instance(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var node, mapName, cluster, desired, observed, health string
	var lastError *string
	var observedAt time.Time
	err := s.db.QueryRow(r.Context(), `SELECT i.node_id,i.map_name,i.cluster_id,i.desired_state,COALESCE(st.observed_state,'unknown'),COALESCE(st.health,'unknown'),st.last_error,COALESCE(st.observed_at,i.updated_at) FROM instances i LEFT JOIN instance_status st ON st.instance_id=i.id WHERE i.id=$1`, id).Scan(&node, &mapName, &cluster, &desired, &observed, &health, &lastError, &observedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found", "instance not found")
		return
	}
	if err != nil {
		writeError(w, 500, "internal_error", "could not read instance")
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "node_id": node, "map": mapName, "map_name": mapName, "cluster_id": cluster, "desired_state": desired, "observed_state": observed, "health": health, "last_error": lastError, "observed_at": observedAt})
}

func (s *server) createInstance(w http.ResponseWriter, r *http.Request) {
	p := currentPrincipal(r)
	if p.Role == "viewer" {
		s.denied(w, r, "instance.create")
		return
	}
	var input struct {
		ID           string `json:"id"`
		NodeID       string `json:"node_id"`
		ClusterID    string `json:"cluster_id"`
		Map          string `json:"map"`
		DesiredState string `json:"desired_state"`
	}
	if !decodeJSON(w, r, &input) || strings.TrimSpace(input.ID) == "" || strings.TrimSpace(input.NodeID) == "" || strings.TrimSpace(input.ClusterID) == "" {
		writeError(w, 400, "invalid_request", "id, node_id, and cluster_id are required")
		return
	}
	if input.Map == "" {
		input.Map = "TheIsland_WP"
	}
	if input.DesiredState == "" {
		input.DesiredState = "stopped"
	}
	if input.DesiredState != "running" && input.DesiredState != "stopped" {
		writeError(w, 400, "invalid_request", "desired_state must be running or stopped")
		return
	}
	_, err := s.db.Exec(r.Context(), `INSERT INTO instances (id,node_id,map_name,cluster_id,desired_state) VALUES ($1,$2,$3,$4,$5)`, input.ID, input.NodeID, input.Map, input.ClusterID, input.DesiredState)
	if err != nil {
		writeError(w, 409, "conflict", "instance or node does not exist")
		return
	}
	_, _ = s.db.Exec(r.Context(), `INSERT INTO instance_status (instance_id) VALUES ($1) ON CONFLICT DO NOTHING`, input.ID)
	s.recordAudit(r.Context(), p.Username, "instance.create", "instance:"+input.ID, map[string]any{"outcome": "allowed"})
	writeJSON(w, 201, map[string]any{"id": input.ID, "node_id": input.NodeID, "map": input.Map, "map_name": input.Map, "cluster_id": input.ClusterID, "desired_state": input.DesiredState, "observed_state": "unknown", "health": "unknown"})
}

func (s *server) updateInstance(w http.ResponseWriter, r *http.Request) {
	p := currentPrincipal(r)
	if p.Role == "viewer" {
		s.denied(w, r, "instance.update")
		return
	}
	var input struct {
		DesiredState string `json:"desired_state"`
		Map          string `json:"map"`
		ClusterID    string `json:"cluster_id"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	id := chi.URLParam(r, "id")
	if input.DesiredState != "" && input.DesiredState != "running" && input.DesiredState != "stopped" {
		writeError(w, 400, "invalid_request", "desired_state must be running or stopped")
		return
	}
	_, err := s.db.Exec(r.Context(), `UPDATE instances SET desired_state=COALESCE(NULLIF($2,''),desired_state),map_name=COALESCE(NULLIF($3,''),map_name),cluster_id=COALESCE(NULLIF($4,''),cluster_id),updated_at=now() WHERE id=$1`, id, input.DesiredState, input.Map, input.ClusterID)
	if err != nil {
		writeError(w, 500, "internal_error", "could not update instance")
		return
	}
	var exists bool
	if err := s.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM instances WHERE id=$1)`, id).Scan(&exists); err != nil || !exists {
		writeError(w, 404, "not_found", "instance not found")
		return
	}
	s.recordAudit(r.Context(), p.Username, "instance.update", "instance:"+id, map[string]any{"outcome": "allowed"})
	s.instance(w, r)
}
func (s *server) action(w http.ResponseWriter, r *http.Request) {
	p := currentPrincipal(r)
	if p.Role == "viewer" {
		s.denied(w, r, "instance.action")
		return
	}
	action := chi.URLParam(r, "action")
	if !map[string]bool{"start": true, "stop": true, "restart": true, "update": true, "backup": true, "restore": true}[action] {
		writeError(w, 404, "not_found", "unsupported action")
		return
	}
	var input struct {
		BackupID string `json:"backup_id"`
	}
	if r.ContentLength != 0 && !decodeJSON(w, r, &input) {
		return
	}
	payloadValues, err := buildActionPayload(action, input.BackupID)
	if err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	id := chi.URLParam(r, "id")
	var exists bool
	if err := s.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM instances WHERE id=$1)`, id).Scan(&exists); err != nil || !exists {
		writeError(w, 404, "not_found", "instance not found")
		return
	}
	jobID, err := randomID()
	if err != nil {
		writeError(w, 500, "internal_error", "could not create job")
		return
	}
	payload, _ := json.Marshal(payloadValues)
	if _, err = s.db.Exec(r.Context(), `INSERT INTO jobs (id,instance_id,kind,payload,created_by) VALUES ($1,$2,$3,$4,$5)`, jobID, id, action, payload, p.ID); err != nil {
		writeError(w, 500, "internal_error", "could not enqueue job")
		return
	}
	if action == "start" || action == "restart" {
		_, _ = s.db.Exec(r.Context(), `UPDATE instances SET desired_state='running',updated_at=now() WHERE id=$1`, id)
	} else if action == "stop" {
		_, _ = s.db.Exec(r.Context(), `UPDATE instances SET desired_state='stopped',updated_at=now() WHERE id=$1`, id)
	}
	s.appendEvent(r.Context(), "job.queued", "job", jobID, map[string]any{"instance_id": id, "kind": action})
	s.recordAudit(r.Context(), p.Username, "instance.action", "instance:"+id, map[string]any{"action": action, "outcome": "allowed", "job_id": jobID})
	writeJSON(w, 202, map[string]any{"id": jobID, "status": "queued", "instance_id": id, "kind": action})
}

func buildActionPayload(action, backupID string) (map[string]string, error) {
	payload := map[string]string{"action": action}
	if backupID != "" {
		payload["backup_id"] = backupID
	}
	if action == "restore" && strings.TrimSpace(backupID) == "" {
		return nil, errors.New("backup_id is required for restore")
	}
	return payload, nil
}

func (s *server) jobs(w http.ResponseWriter, r *http.Request) {
	limit := parseLimit(r.URL.Query().Get("limit"))
	parts, err := decodeCursor(r.URL.Query().Get("cursor"), 2)
	if cursorError(w, err) {
		return
	}
	query := `SELECT id,instance_id,kind,status,attempts,last_error,created_by,created_at,started_at,finished_at FROM jobs`
	args := []any{limit + 1}
	if len(parts) == 2 {
		created, parseErr := time.Parse(time.RFC3339Nano, parts[0])
		if parseErr != nil {
			cursorError(w, parseErr)
			return
		}
		query += ` WHERE (created_at,id) < ($2,$3)`
		args = append(args, created, parts[1])
	}
	query += ` ORDER BY created_at DESC,id DESC LIMIT $1`
	rows, err := s.db.Query(r.Context(), query, args...)
	if err != nil {
		writeError(w, 500, "internal_error", "could not list jobs")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0, limit+1)
	for rows.Next() {
		var id, kind, status string
		var instanceID, lastError, createdBy *string
		var attempts int
		var createdAt time.Time
		var startedAt, finishedAt *time.Time
		if err := rows.Scan(&id, &instanceID, &kind, &status, &attempts, &lastError, &createdBy, &createdAt, &startedAt, &finishedAt); err != nil {
			writeError(w, 500, "internal_error", "could not read jobs")
			return
		}
		items = append(items, map[string]any{"id": id, "instance_id": instanceID, "kind": kind, "status": status, "attempts": attempts, "last_error": lastError, "created_by": createdBy, "created_at": createdAt, "started_at": startedAt, "finished_at": finishedAt})
	}
	next := ""
	if len(items) > limit {
		created := items[limit-1]["created_at"].(time.Time)
		next = encodeCursor(created.Format(time.RFC3339Nano), items[limit-1]["id"].(string))
		items = items[:limit]
	}
	writeJSON(w, 200, map[string]any{"items": items, "next_cursor": next})
}
func (s *server) job(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var kind, status string
	var instanceID, lastError, createdBy *string
	var attempts int
	var createdAt time.Time
	err := s.db.QueryRow(r.Context(), `SELECT kind,status,instance_id,attempts,last_error,created_by,created_at FROM jobs WHERE id=$1`, id).Scan(&kind, &status, &instanceID, &attempts, &lastError, &createdBy, &createdAt)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found", "job not found")
		return
	}
	if err != nil {
		writeError(w, 500, "internal_error", "could not read job")
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "kind": kind, "status": status, "instance_id": instanceID, "attempts": attempts, "last_error": lastError, "created_by": createdBy, "created_at": createdAt})
}
func (s *server) audit(w http.ResponseWriter, r *http.Request) {
	limit := parseLimit(r.URL.Query().Get("limit"))
	parts, err := decodeCursor(r.URL.Query().Get("cursor"), 1)
	if cursorError(w, err) {
		return
	}
	query := `SELECT id,actor,action,resource,metadata,created_at FROM audit_log`
	args := []any{limit + 1}
	if len(parts) == 1 {
		var cursorID int64
		if _, scanErr := fmt.Sscan(parts[0], &cursorID); scanErr != nil {
			cursorError(w, scanErr)
			return
		}
		query += ` WHERE id < $2`
		args = append(args, cursorID)
	}
	query += ` ORDER BY id DESC LIMIT $1`
	rows, err := s.db.Query(r.Context(), query, args...)
	if err != nil {
		writeError(w, 500, "internal_error", "could not list audit records")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0, limit+1)
	for rows.Next() {
		var id int64
		var actor, action, resource string
		var metadata []byte
		var at time.Time
		if err := rows.Scan(&id, &actor, &action, &resource, &metadata, &at); err != nil {
			writeError(w, 500, "internal_error", "could not read audit records")
			return
		}
		var decoded any
		_ = json.Unmarshal(metadata, &decoded)
		items = append(items, map[string]any{"id": id, "actor": actor, "action": action, "resource": resource, "metadata": decoded, "created_at": at})
	}
	next := ""
	if len(items) > limit {
		next = encodeCursor(fmt.Sprint(items[limit-1]["id"]))
		items = items[:limit]
	}
	writeJSON(w, 200, map[string]any{"items": items, "next_cursor": next})
}
func (s *server) users(w http.ResponseWriter, r *http.Request) {
	limit := parseLimit(r.URL.Query().Get("limit"))
	parts, err := decodeCursor(r.URL.Query().Get("cursor"), 2)
	if cursorError(w, err) {
		return
	}
	query := `SELECT id,username,role,disabled_at,created_at FROM users`
	args := []any{limit + 1}
	if len(parts) == 2 {
		query += ` WHERE (username,id) > ($2,$3)`
		args = append(args, parts[0], parts[1])
	}
	query += ` ORDER BY username,id LIMIT $1`
	rows, err := s.db.Query(r.Context(), query, args...)
	if err != nil {
		writeError(w, 500, "internal_error", "could not list users")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0, limit+1)
	for rows.Next() {
		var id, username, role string
		var disabled *time.Time
		var created time.Time
		if err := rows.Scan(&id, &username, &role, &disabled, &created); err != nil {
			writeError(w, 500, "internal_error", "could not read users")
			return
		}
		items = append(items, map[string]any{"id": id, "username": username, "role": role, "disabled_at": disabled, "created_at": created})
	}
	next := ""
	if len(items) > limit {
		next = encodeCursor(items[limit-1]["username"].(string), items[limit-1]["id"].(string))
		items = items[:limit]
	}
	writeJSON(w, 200, map[string]any{"items": items, "next_cursor": next})
}
func (s *server) createUser(w http.ResponseWriter, r *http.Request) {
	var input struct{ Username, Password, Role string }
	if !decodeJSON(w, r, &input) || strings.TrimSpace(input.Username) == "" || len(input.Password) < 12 || !map[string]bool{"admin": true, "operator": true, "viewer": true}[input.Role] {
		writeError(w, 400, "invalid_request", "username, role, and a 12-character password are required")
		return
	}
	hash, err := hashPassword(input.Password)
	if err != nil {
		writeError(w, 500, "internal_error", "could not hash password")
		return
	}
	id, _ := randomID()
	if _, err = s.db.Exec(r.Context(), `INSERT INTO users (id,username,password_hash,role) VALUES ($1,$2,$3,$4)`, id, input.Username, hash, input.Role); err != nil {
		writeError(w, 409, "conflict", "username already exists")
		return
	}
	p := currentPrincipal(r)
	s.recordAudit(r.Context(), p.Username, "user.create", "user:"+input.Username, map[string]any{"role": input.Role, "outcome": "allowed"})
	writeJSON(w, 201, map[string]any{"id": id, "username": input.Username, "role": input.Role})
}

func (s *server) updateUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var input struct {
		Role     *string `json:"role"`
		Disabled *bool   `json:"disabled"`
	}
	if !decodeJSON(w, r, &input) || (input.Role == nil && input.Disabled == nil) {
		writeError(w, 400, "invalid_request", "role or disabled is required")
		return
	}
	if input.Role != nil && !validRole(*input.Role) {
		writeError(w, 400, "invalid_request", "invalid role")
		return
	}
	var username, currentRole string
	var disabledAt *time.Time
	if err := s.db.QueryRow(r.Context(), `SELECT username,role,disabled_at FROM users WHERE id=$1`, id).Scan(&username, &currentRole, &disabledAt); errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found", "user not found")
		return
	} else if err != nil {
		writeError(w, 500, "internal_error", "could not read user")
		return
	}
	p := currentPrincipal(r)
	if input.Role != nil && !currentUserRoleChangeAllowed(id, p.ID, *input.Role, currentRole) {
		writeError(w, 409, "conflict", "cannot change the current user's role")
		return
	}
	if input.Disabled != nil && *input.Disabled && id == p.ID {
		writeError(w, 409, "conflict", "cannot disable the current user")
		return
	}
	removesActiveAdmin := input.Disabled != nil && *input.Disabled
	if input.Role != nil && *input.Role != "admin" {
		removesActiveAdmin = true
	}
	if removesActiveAdmin && currentRole == "admin" && disabledAt == nil {
		var activeAdmins int
		if err := s.db.QueryRow(r.Context(), `SELECT count(*) FROM users WHERE role='admin' AND disabled_at IS NULL`).Scan(&activeAdmins); err != nil {
			writeError(w, 500, "internal_error", "could not count active administrators")
			return
		}
		if activeAdmins <= 1 {
			writeError(w, 409, "conflict", "cannot disable the last active administrator")
			return
		}
	}
	_, err := s.db.Exec(r.Context(), `UPDATE users SET role=COALESCE($2,role), disabled_at=CASE WHEN $3::boolean IS NULL THEN disabled_at WHEN $3 THEN now() ELSE NULL END, updated_at=now() WHERE id=$1`, id, input.Role, input.Disabled)
	if err != nil {
		writeError(w, 500, "internal_error", "could not update user")
		return
	}
	s.recordAudit(r.Context(), p.Username, "user.update", "user:"+username, map[string]any{"role": input.Role, "disabled": input.Disabled, "outcome": "allowed"})
	var role string
	var updatedDisabled *time.Time
	var created time.Time
	if err := s.db.QueryRow(r.Context(), `SELECT role,disabled_at,created_at FROM users WHERE id=$1`, id).Scan(&role, &updatedDisabled, &created); err != nil {
		writeError(w, 500, "internal_error", "could not read updated user")
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "username": username, "role": role, "disabled_at": updatedDisabled, "created_at": created})
}

func (s *server) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := s.sessionPrincipal(r)
		if !ok {
			writeError(w, 401, "unauthorized", "authentication required")
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			token := r.Header.Get("X-CSRF-Token")
			csrf, err := r.Cookie(csrfCookie)
			session, sessionErr := r.Cookie(sessionCookie)
			var storedHash string
			if sessionErr == nil {
				sessionErr = s.db.QueryRow(r.Context(), `SELECT csrf_hash FROM sessions WHERE id=$1 AND revoked_at IS NULL`, session.Value).Scan(&storedHash)
			}
			if err != nil || sessionErr != nil || token == "" || token != csrf.Value || hashToken(token) != storedHash {
				s.recordAudit(r.Context(), p.Username, "request.csrf_denied", r.URL.Path, map[string]any{"outcome": "denied"})
				writeError(w, 403, "forbidden", "csrf validation failed")
				return
			}
		}
		r = r.WithContext(context.WithValue(r.Context(), principalKey, p))
		next.ServeHTTP(w, r)
	})
}
func (s *server) requireRole(role string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if currentPrincipal(r).Role != role {
				p := currentPrincipal(r)
				s.recordAudit(r.Context(), p.Username, "request.role_denied", r.URL.Path, map[string]any{"required_role": role, "outcome": "denied"})
				writeError(w, 403, "forbidden", "insufficient role")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
func (s *server) denied(w http.ResponseWriter, r *http.Request, what string) {
	p := currentPrincipal(r)
	s.recordAudit(r.Context(), p.Username, what, r.URL.Path, map[string]any{"outcome": "denied"})
	writeError(w, 403, "forbidden", "insufficient role")
}
func (s *server) sessionPrincipal(r *http.Request) (principal, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return principal{}, false
	}
	var p principal
	var expires time.Time
	err = s.db.QueryRow(r.Context(), `SELECT u.id,u.username,u.role,se.expires_at FROM sessions se JOIN users u ON u.id=se.user_id WHERE se.id=$1 AND se.revoked_at IS NULL AND u.disabled_at IS NULL`, c.Value).Scan(&p.ID, &p.Username, &p.Role, &expires)
	p.Capabilities = roleCapabilities(p.Role)
	return p, err == nil && expires.After(time.Now())
}

func roleCapabilities(role string) []string {
	capabilities := []string{"read:instances", "read:jobs", "read:audit"}
	if role == "admin" || role == "operator" {
		capabilities = append(capabilities, "write:instances", "write:jobs")
	}
	if role == "admin" {
		capabilities = append(capabilities, "manage:users", "manage:nodes")
	}
	return capabilities
}
func currentPrincipal(r *http.Request) principal {
	p, _ := r.Context().Value(principalKey).(principal)
	return p
}
func (s *server) websocket(w http.ResponseWriter, r *http.Request) {
	cursor, _ := strconv.ParseInt(r.URL.Query().Get("cursor"), 10, 64)
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	if err := conn.WriteJSON(map[string]any{"type": "ready", "cursor": cursor}); err != nil {
		return
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		rows, queryErr := s.db.Query(r.Context(), `SELECT id,event_type,resource_type,resource_id,payload,created_at FROM event_cursor WHERE id > $1 ORDER BY id LIMIT 100`, cursor)
		if queryErr != nil {
			return
		}
		for rows.Next() {
			var id int64
			var eventType, resourceType, resourceID string
			var payload []byte
			var createdAt time.Time
			if err := rows.Scan(&id, &eventType, &resourceType, &resourceID, &payload, &createdAt); err != nil {
				rows.Close()
				return
			}
			var decoded any
			_ = json.Unmarshal(payload, &decoded)
			if err := conn.WriteJSON(map[string]any{"type": "event", "cursor": id, "event_type": eventType, "resource_type": resourceType, "resource_id": resourceID, "payload": decoded, "created_at": createdAt}); err != nil {
				rows.Close()
				return
			}
			cursor = id
		}
		rows.Close()
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *server) appendEvent(ctx context.Context, eventType, resourceType, resourceID string, payload map[string]any) {
	body, _ := json.Marshal(payload)
	_, _ = s.db.Exec(ctx, `INSERT INTO event_cursor (event_type,resource_type,resource_id,payload) VALUES ($1,$2,$3,$4)`, eventType, resourceType, resourceID, body)
}
func (s *server) recordAudit(ctx context.Context, actor, action, resource string, metadata map[string]any) {
	body, _ := json.Marshal(metadata)
	_, _ = s.db.Exec(ctx, `INSERT INTO audit_log (actor,action,resource,metadata) VALUES ($1,$2,$3,$4)`, actor, action, resource, body)
}
func hashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	sum := argon2.IDKey([]byte(password), salt, 3, 64*1024, 2, 32)
	return fmt.Sprintf("argon2id$v=19$m=65536,t=3,p=2$%s$%s", hex.EncodeToString(salt), hex.EncodeToString(sum)), nil
}
func verifyPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 5 || parts[0] != "argon2id" {
		return false
	}
	salt, err := hex.DecodeString(parts[3])
	if err != nil {
		return false
	}
	expected, err := hex.DecodeString(parts[4])
	if err != nil {
		return false
	}
	actual := argon2.IDKey([]byte(password), salt, 3, 64*1024, 2, uint32(len(expected)))
	if len(actual) != len(expected) {
		return false
	}
	var diff byte
	for i := range actual {
		diff |= actual[i] ^ expected[i]
	}
	return diff == 0
}
func randomID() (string, error) {
	buf := make([]byte, 18)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
func hashToken(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func (s *server) setCookie(w http.ResponseWriter, name, value string, httpOnly bool, expires time.Time) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", Expires: expires, HttpOnly: httpOnly, Secure: s.secure, SameSite: http.SameSiteLaxMode})
}
func parseLimit(value string) int {
	n, _ := strconv.Atoi(value)
	if n < 1 || n > 100 {
		n = 50
	}
	return n
}

func encodeCursor(parts ...string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strings.Join(parts, "\x00")))
}

func decodeCursor(value string, expected int) ([]string, error) {
	if value == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, errors.New("invalid cursor")
	}
	parts := strings.Split(string(raw), "\x00")
	if len(parts) != expected {
		return nil, errors.New("invalid cursor")
	}
	return parts, nil
}

func cursorError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	writeError(w, 400, "invalid_cursor", "cursor is invalid or expired")
	return true
}
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeError(w, 400, "invalid_request", "invalid JSON body")
		return false
	}
	return true
}
func writeError(w http.ResponseWriter, status int, code, message string) {
	requestID := w.Header().Get("X-Request-ID")
	errorBody := map[string]string{"code": code, "message": message}
	if requestID != "" {
		errorBody["request_id"] = requestID
	}
	writeJSON(w, status, map[string]any{"error": errorBody})
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil && !errors.Is(err, http.ErrAbortHandler) {
		log.Printf("write response: %v", err)
	}
}
func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
