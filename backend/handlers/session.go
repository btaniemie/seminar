package handlers

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/seminar/backend/hub"
	"github.com/seminar/backend/store"
)

// SessionHandler handles session lifecycle REST endpoints.
type SessionHandler struct {
	hub   *hub.Hub
	store *store.Store
}

func NewSessionHandler(h *hub.Hub, st *store.Store) *SessionHandler {
	return &SessionHandler{hub: h, store: st}
}

// CreateSession handles POST /api/session.
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

	// GetOrCreate starts the session's run() goroutine and sets createdAt.
	// Redis is written on first client join via enqueueMeta; nothing to write yet.
	sh.hub.GetOrCreate(id)

	writeJSON(w, http.StatusCreated, map[string]string{"sessionId": id})
}

// GetSession handles GET /api/session/{id}.
// Returns session metadata as JSON: { sessionId, mode, highlightCount, chatCount, createdAt, updatedAt }.
// The extension uses this to validate a join link and display session info.
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

	// One pipeline round-trip: HGetAll + LLen highlights + LLen chat.
	// On Redis error, fall back to the in-memory hub so a hiccup never breaks join-link validation.
	summary, exists, err := sh.store.GetSessionSummary(r.Context(), id)
	if err != nil {
		slog.Warn("GetSessionSummary failed, falling back to hub", "session", id, "err", err)
	}

	if exists {
		writeJSON(w, http.StatusOK, map[string]any{
			"sessionId":      id,
			"mode":           summary.Meta.Mode,
			"highlightCount": summary.HighlightCount,
			"chatCount":      summary.ChatCount,
			"createdAt":      summary.Meta.CreatedAt.UTC().Format(time.RFC3339),
			"updatedAt":      summary.Meta.UpdatedAt.UTC().Format(time.RFC3339),
		})
		return
	}

	// Not in Redis — fall back to in-memory hub (new session not yet persisted,
	// predates Phase 10, or Redis temporarily unavailable).
	sess := sh.hub.Get(id)
	if sess == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sessionId":      id,
		"mode":           sess.GetMode(),
		"highlightCount": 0,
		"chatCount":      0,
		"createdAt":      sess.CreatedAt().UTC().Format(time.RFC3339),
		"updatedAt":      sess.CreatedAt().UTC().Format(time.RFC3339),
	})
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
