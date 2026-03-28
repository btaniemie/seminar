package hub

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"time"
)

const (
	sessionsDir = "./sessions"
	sessionTTL  = 7 * 24 * time.Hour
)

type sessionSnapshot struct {
	ID           string           `json:"id"`
	Mode         string           `json:"mode"`
	HostID       string           `json:"hostId"`
	HighlightBuf []HighlightEntry `json:"highlights"`
	UpdatedAt    time.Time        `json:"updatedAt"`
}

// persist writes the session state to disk asynchronously.
// Designed to be called with `go s.persist()`.
func (s *Session) persist() {
	s.mu.RLock()
	snap := sessionSnapshot{
		ID:           s.id,
		Mode:         s.mode,
		HostID:       s.hostID,
		HighlightBuf: make([]HighlightEntry, len(s.highlightBuf)),
		UpdatedAt:    time.Now(),
	}
	copy(snap.HighlightBuf, s.highlightBuf)
	s.mu.RUnlock()

	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		log.Printf("[persist] mkdir failed: %v", err)
		return
	}

	data, err := json.Marshal(snap)
	if err != nil {
		log.Printf("[persist] marshal failed for session %s: %v", s.id, err)
		return
	}

	path := filepath.Join(sessionsDir, s.id+".json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		log.Printf("[persist] write failed for session %s: %v", s.id, err)
	}
}

// LoadSessions reads all session files from disk and restores them into the hub.
// Stale files older than sessionTTL are deleted first.
// Call this once at startup before accepting connections.
func LoadSessions(h *Hub) {
	cleanOldSessions()

	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("[persist] readdir failed: %v", err)
		}
		return
	}

	loaded := 0
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		path := filepath.Join(sessionsDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			log.Printf("[persist] read failed for %s: %v", entry.Name(), err)
			continue
		}

		var snap sessionSnapshot
		if err := json.Unmarshal(data, &snap); err != nil {
			log.Printf("[persist] unmarshal failed for %s: %v", entry.Name(), err)
			continue
		}

		// GetOrCreate starts the session's run() goroutine; then we populate state.
		sess := h.GetOrCreate(snap.ID)
		sess.mu.Lock()
		sess.mode = snap.Mode
		sess.hostID = snap.HostID
		sess.highlightBuf = snap.HighlightBuf
		sess.mu.Unlock()
		loaded++
	}

	if loaded > 0 {
		log.Printf("[persist] restored %d session(s) from disk", loaded)
	}
}

func cleanOldSessions() {
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return // directory may not exist yet — that's fine
	}

	cutoff := time.Now().Add(-sessionTTL)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			path := filepath.Join(sessionsDir, entry.Name())
			log.Printf("[persist] removing stale session file %s", entry.Name())
			os.Remove(path)
		}
	}
}
