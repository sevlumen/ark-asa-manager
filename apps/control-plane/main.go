package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
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

func main() {
	ctx := context.Background()
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
	if err := s.startInternalListener(); err != nil {
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
			r.Get("/me", s.me)
			r.Get("/system/health", s.systemHealth)
			r.Get("/instances", s.instances)
			r.Post("/instances/{id}/actions/{action}", s.action)
			r.Get("/jobs", s.jobs)
			r.Get("/jobs/{id}", s.job)
			r.Get("/audit", s.audit)
			r.Get("/ws", s.websocket)
			r.Group(func(r chi.Router) {
				r.Use(s.requireRole("admin"))
				r.Get("/users", s.users)
				r.Post("/users", s.createUser)
			})
		})
	})
	log.Printf("control-plane listening on :%s", port)
	if err := http.ListenAndServe(":"+port, r); err != nil {
		log.Fatal(err)
	}
}

func (s *server) startInternalListener() error {
	certFile, keyFile, caFile := os.Getenv("INTERNAL_TLS_CERT_FILE"), os.Getenv("INTERNAL_TLS_KEY_FILE"), os.Getenv("INTERNAL_TLS_CA_FILE")
	if certFile == "" || keyFile == "" || caFile == "" {
		log.Print("internal agent listener disabled: TLS files are not configured")
		return nil
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return fmt.Errorf("load internal TLS certificate: %w", err)
	}
	caBytes, err := os.ReadFile(caFile)
	if err != nil {
		return fmt.Errorf("read internal TLS CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caBytes) {
		return errors.New("parse internal TLS CA")
	}
	internal := chi.NewRouter()
	internal.Post("/internal/agent/lease", s.agentLease)
	internal.Post("/internal/agent/jobs/{id}/complete", s.agentComplete)
	internal.Post("/internal/agent/jobs/{id}/fail", s.agentFail)
	internal.Post("/internal/agent/heartbeat", s.agentHeartbeat)
	listener, err := net.Listen("tcp", ":"+getenv("INTERNAL_PORT", "8443"))
	if err != nil {
		return err
	}
	tlsListener := tls.NewListener(listener, &tls.Config{Certificates: []tls.Certificate{cert}, ClientCAs: pool, ClientAuth: tls.RequireAndVerifyClientCert, MinVersion: tls.VersionTLS12})
	go func() {
		log.Printf("private agent listener listening on :%s", getenv("INTERNAL_PORT", "8443"))
		if err := http.Serve(tlsListener, internal); err != nil {
			log.Printf("private agent listener stopped: %v", err)
		}
	}()
	return nil
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
	result, err := s.db.Exec(r.Context(), `UPDATE jobs SET status=CASE WHEN attempts>=max_attempts THEN 'failed' ELSE 'queued' END,lease_owner=NULL,lease_expires_at=NULL,last_error=$3,finished_at=CASE WHEN attempts>=max_attempts THEN now() ELSE NULL END WHERE id=$1 AND lease_owner=$2`, id, nodeID, input.Error)
	if err != nil {
		writeError(w, 500, "internal_error", "could not fail job")
		return
	}
	if result.RowsAffected() != 1 {
		writeError(w, 409, "conflict", "job lease is no longer owned by this agent")
		return
	}
	s.appendEvent(r.Context(), "job.failed", "job", id, map[string]any{"owner": nodeID, "error": input.Error})
	writeJSON(w, 200, map[string]string{"status": "failed", "id": id})
}

func (s *server) agentHeartbeat(w http.ResponseWriter, r *http.Request) {
	nodeID, ok := s.agentIdentity(r)
	if !ok {
		writeError(w, 403, "forbidden", "unknown agent identity")
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
	writeJSON(w, 200, map[string]any{"node_id": nodeID, "status": "online", "observed_at": time.Now().UTC()})
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
	status := "healthy"
	if err := s.db.Ping(ctx); err != nil {
		status = "critical"
	}
	writeJSON(w, 200, map[string]any{"status": status, "control_plane": status, "database": status, "observed_at": time.Now().UTC()})
}

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	var input struct{ Username, Password string }
	if !decodeJSON(w, r, &input) || input.Username == "" || input.Password == "" {
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
		s.recordAudit(r.Context(), "anonymous", "auth.login_failed", "user:"+input.Username, map[string]any{"outcome": "denied"})
		writeError(w, 401, "unauthorized", "invalid credentials")
		return
	}
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
	rows, err := s.db.Query(r.Context(), `SELECT i.id,i.node_id,i.map_name,i.cluster_id,i.desired_state,COALESCE(st.observed_state,'unknown'),COALESCE(st.health,'unknown'),st.last_error,COALESCE(st.observed_at,i.updated_at) FROM instances i LEFT JOIN instance_status st ON st.instance_id=i.id ORDER BY i.id`)
	if err != nil {
		writeError(w, 500, "internal_error", "could not list instances")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, node, mapName, cluster, desired, observed, health string
		var lastError *string
		var observedAt time.Time
		if err := rows.Scan(&id, &node, &mapName, &cluster, &desired, &observed, &health, &lastError, &observedAt); err != nil {
			writeError(w, 500, "internal_error", "could not read instances")
			return
		}
		items = append(items, map[string]any{"id": id, "node_id": node, "map_name": mapName, "cluster_id": cluster, "desired_state": desired, "observed_state": observed, "health": health, "last_error": lastError, "observed_at": observedAt})
	}
	writeJSON(w, 200, map[string]any{"items": items, "count": len(items)})
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
	payload, _ := json.Marshal(map[string]string{"action": action})
	if _, err = s.db.Exec(r.Context(), `INSERT INTO jobs (id,instance_id,kind,payload,created_by) VALUES ($1,$2,$3,$4,$5)`, jobID, id, action, payload, p.ID); err != nil {
		writeError(w, 500, "internal_error", "could not enqueue job")
		return
	}
	s.appendEvent(r.Context(), "job.queued", "job", jobID, map[string]any{"instance_id": id, "kind": action})
	s.recordAudit(r.Context(), p.Username, "instance.action", "instance:"+id, map[string]any{"action": action, "outcome": "allowed", "job_id": jobID})
	writeJSON(w, 202, map[string]any{"id": jobID, "status": "queued", "instance_id": id, "kind": action})
}

func (s *server) jobs(w http.ResponseWriter, r *http.Request) {
	limit := parseLimit(r.URL.Query().Get("limit"))
	rows, err := s.db.Query(r.Context(), `SELECT id,instance_id,kind,status,attempts,last_error,created_by,created_at,started_at,finished_at FROM jobs ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		writeError(w, 500, "internal_error", "could not list jobs")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
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
	writeJSON(w, 200, map[string]any{"items": items})
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
	rows, err := s.db.Query(r.Context(), `SELECT id,actor,action,resource,metadata,created_at FROM audit_log ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		writeError(w, 500, "internal_error", "could not list audit records")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
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
	writeJSON(w, 200, map[string]any{"items": items})
}
func (s *server) users(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `SELECT id,username,role,disabled_at,created_at FROM users ORDER BY username`)
	if err != nil {
		writeError(w, 500, "internal_error", "could not list users")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
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
	writeJSON(w, 200, map[string]any{"items": items})
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
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeError(w, 400, "invalid_request", "invalid JSON body")
		return false
	}
	return true
}
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
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
