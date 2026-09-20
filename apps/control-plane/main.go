package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgxpool"
)

type server struct {
	db       *pgxpool.Pool
	upgrader websocket.Upgrader
}

func main() {
	ctx := context.Background()
	healthcheck := flag.Bool("healthcheck", false, "check the local HTTP server and exit")
	flag.Parse()
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	if *healthcheck {
		response, err := http.Get("http://127.0.0.1:" + port + "/healthz")
		if err != nil || response.StatusCode != http.StatusOK {
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

	s := &server{db: db, upgrader: websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}}
	r := chi.NewRouter()
	r.Get("/healthz", s.health)
	r.Get("/readyz", s.ready)
	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/instances", s.instances)
		r.Get("/ws", s.websocket)
	})

	log.Printf("control-plane listening on :%s", port)
	if err := http.ListenAndServe(":"+port, r); err != nil {
		log.Fatal(err)
	}
}

func (s *server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.db.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready", "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *server) instances(w http.ResponseWriter, r *http.Request) {
	if err := s.db.Ping(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": []any{}, "count": 0})
}

func (s *server) websocket(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	_ = conn.WriteJSON(map[string]string{"type": "ready"})
	for {
		messageType, message, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if err := conn.WriteMessage(messageType, message); err != nil {
			return
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil && !errors.Is(err, http.ErrAbortHandler) {
		log.Printf("write response: %v", err)
	}
}
