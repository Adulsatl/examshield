package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

// In-memory stores for PoC (replace with DB later)
var (
	agentsMu sync.RWMutex
	agents   = map[string]*Agent{}
	tokens   = map[string]string{} // token -> agentID
    eventsMu sync.RWMutex
    events   = make([]Event, 0, 1024)
)

type Agent struct {
	ID         string    `json:"id"`
	Hostname   string    `json:"hostname"`
	OS         string    `json:"os"`
	Version    string    `json:"version"`
	LastSeenAt time.Time `json:"last_seen_at"`
}

type enrollRequest struct {
	Hostname string `json:"hostname"`
	OS       string `json:"os"`
	Version  string `json:"version"`
}

type enrollResponse struct {
	AgentID string `json:"agent_id"`
	Token   string `json:"token"`
}

type heartbeatRequest struct{
	AgentID string `json:"agent_id"`
}

type configResponse struct{
	Policy map[string]any `json:"policy"`
}

type Event struct {
    ID        string         `json:"id"`
    AgentID   string         `json:"agent_id"`
    Type      string         `json:"type"`
    Payload   map[string]any `json:"payload"`
    CreatedAt time.Time      `json:"created_at"`
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("/agents/enroll", enrollHandler)
	mux.HandleFunc("/agents/heartbeat", heartbeatHandler)
	mux.HandleFunc("/config", configHandler)
    mux.HandleFunc("/events", eventsHandler)

	addr := ":8080"
	if v := os.Getenv("PORT"); v != "" {
		addr = ":" + v
	}
	log.Printf("ExamShield EDU API listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, withJSON(withLogging(mux))))
}

func enrollHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req enrollRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	id := randID()
	token := randID()
	agent := &Agent{ID: id, Hostname: req.Hostname, OS: req.OS, Version: req.Version, LastSeenAt: time.Now()}

	agentsMu.Lock()
	agents[id] = agent
	tokens[token] = id
	agentsMu.Unlock()

	writeJSON(w, http.StatusOK, enrollResponse{AgentID: id, Token: token})
}

func heartbeatHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	tok := r.Header.Get("X-Agent-Token")
	if tok == "" {
		http.Error(w, "missing token", http.StatusUnauthorized)
		return
	}
	agentsMu.Lock()
	id, ok := tokens[tok]
	if !ok {
		agentsMu.Unlock()
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}
	var req heartbeatRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.AgentID != "" && req.AgentID != id {
		agentsMu.Unlock()
		http.Error(w, "agent mismatch", http.StatusUnauthorized)
		return
	}
	if a := agents[id]; a != nil {
		a.LastSeenAt = time.Now()
	}
	agentsMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"status":"ok"})
}

func configHandler(w http.ResponseWriter, r *http.Request) {
	// For PoC, return a static policy
	resp := configResponse{Policy: map[string]any{
		"usb_block": true,
		"app_blacklist": []string{"chrome", "msedge", "firefox", "brave"},
		"app_whitelist": []string{"notepad", "code"},
		"wifi_mode": "off",
		"screenshot_on_block": true,
	}}
	writeJSON(w, http.StatusOK, resp)
}

func eventsHandler(w http.ResponseWriter, r *http.Request) {
    switch r.Method {
    case http.MethodPost:
        tok := r.Header.Get("X-Agent-Token")
        if tok == "" {
            http.Error(w, "missing token", http.StatusUnauthorized)
            return
        }
        agentsMu.RLock()
        agentID, ok := tokens[tok]
        agentsMu.RUnlock()
        if !ok {
            http.Error(w, "invalid token", http.StatusUnauthorized)
            return
        }
        var e Event
        if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
            http.Error(w, "invalid json", http.StatusBadRequest)
            return
        }
        e.ID = randID()
        if e.AgentID == "" { e.AgentID = agentID }
        e.CreatedAt = time.Now()
        eventsMu.Lock()
        events = append(events, e)
        // Keep last 1000 events
        if len(events) > 1000 {
            events = events[len(events)-1000:]
        }
        eventsMu.Unlock()
        writeJSON(w, http.StatusOK, map[string]string{"status":"ok", "id": e.ID})
    case http.MethodGet:
        // return recent events
        eventsMu.RLock()
        out := make([]Event, len(events))
        copy(out, events)
        eventsMu.RUnlock()
        writeJSON(w, http.StatusOK, out)
    default:
        w.WriteHeader(http.StatusMethodNotAllowed)
    }
}

// middleware
func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start))
	})
}

func withJSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		next.ServeHTTP(w, r)
	})
}

func randID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
