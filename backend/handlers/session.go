package handlers

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/seminar/backend/hub"
)

// SessionHandler handles session lifecycle REST endpoints.
type SessionHandler struct {
	hub *hub.Hub
}

func NewSessionHandler(h *hub.Hub) *SessionHandler {
	return &SessionHandler{hub: h}
}

// CreateSession handles POST /api/session
// Returns a new session ID that clients can share.
func (sh *SessionHandler) CreateSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id, err := generateSessionID()
	if err != nil {
		http.Error(w, "failed to generate session id", http.StatusInternalServerError)
		return
	}

	// Creating the session eagerly so it shows up in Exists checks immediately.
	sh.hub.GetOrCreate(id)

	writeJSON(w, http.StatusCreated, map[string]string{"sessionId": id})
}

// GetSession handles GET /api/session/{id}
// Returns 200 if the session exists, 404 otherwise.
// The extension uses this to validate a session before joining.
func (sh *SessionHandler) GetSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}

	if !sh.hub.Exists(id) {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"sessionId": id, "status": "active"})
}

// generateSessionID returns a URL-safe random 8-character hex string (4 bytes → 8 hex chars).
func generateSessionID() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", b), nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
