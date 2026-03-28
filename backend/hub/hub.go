package hub

import (
	"sync"
)

// Hub manages all active sessions. It is safe for concurrent use.
type Hub struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

func NewHub() *Hub {
	return &Hub{
		sessions: make(map[string]*Session),
	}
}

// GetOrCreate returns an existing session or creates a new one.
func (h *Hub) GetOrCreate(sessionID string) *Session {
	h.mu.Lock()
	defer h.mu.Unlock()

	s, ok := h.sessions[sessionID]
	if !ok {
		s = newSession(sessionID)
		h.sessions[sessionID] = s
		go s.run()
	}
	return s
}

// Exists reports whether a session with the given ID is active.
func (h *Hub) Exists(sessionID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.sessions[sessionID]
	return ok
}

// Get returns the session for the given ID, or nil if it does not exist.
// Unlike GetOrCreate, this never creates a new session.
func (h *Hub) Get(sessionID string) *Session {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.sessions[sessionID]
}

// remove is called by a session when its last client disconnects.
func (h *Hub) remove(sessionID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.sessions, sessionID)
}
