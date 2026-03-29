package hub

import (
	"sync"

	"github.com/seminar/backend/store"
)

// DivergenceFunc is called (in its own goroutine) after each highlight is added.
// all contains the full current highlight buffer; newEntry is the one just added.
type DivergenceFunc func(sessionID string, all []HighlightEntry, newEntry HighlightEntry)

// Hub manages all active sessions. It is safe for concurrent use.
type Hub struct {
	mu             sync.RWMutex
	sessions       map[string]*Session
	store          *store.Store
	divMu          sync.RWMutex
	divergenceFn   DivergenceFunc
}

func NewHub(st *store.Store) *Hub {
	return &Hub{
		sessions: make(map[string]*Session),
		store:    st,
	}
}

// SetDivergenceFunc registers a function to call after each highlight is added.
// Safe to call before or after any sessions are created.
func (h *Hub) SetDivergenceFunc(fn DivergenceFunc) {
	h.divMu.Lock()
	h.divergenceFn = fn
	h.divMu.Unlock()
}

// DivergenceFn returns the registered divergence function, or nil.
func (h *Hub) DivergenceFn() DivergenceFunc {
	h.divMu.RLock()
	defer h.divMu.RUnlock()
	return h.divergenceFn
}

// GetOrCreate returns an existing session or creates a new one.
func (h *Hub) GetOrCreate(sessionID string) *Session {
	h.mu.Lock()
	defer h.mu.Unlock()

	s, ok := h.sessions[sessionID]
	if !ok {
		s = newSession(sessionID, h.store, h)
		h.sessions[sessionID] = s
		go s.run()
	}
	return s
}

// Exists reports whether a session with the given ID is active in memory.
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
