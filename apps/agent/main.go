package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	controlPlane := os.Getenv("CONTROL_PLANE_URL")
	if controlPlane == "" {
		controlPlane = "http://control-plane:8080"
	}
	nodeID := os.Getenv("NODE_ID")
	if nodeID == "" {
		nodeID = "local-node"
	}
	client := &http.Client{Timeout: 5 * time.Second}
	http.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "node_id": nodeID})
	})
	http.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		request, _ := http.NewRequest(http.MethodGet, controlPlane+"/healthz", nil)
		response, err := client.Do(request)
		if err != nil || response.StatusCode >= 500 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		defer response.Body.Close()
		w.WriteHeader(http.StatusOK)
	})
	port := os.Getenv("AGENT_PORT")
	if port == "" {
		port = "8090"
	}
	log.Printf("agent %s listening on :%s", nodeID, port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
