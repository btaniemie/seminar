package hub

import (
	"context"
	"log/slog"
	"time"

	"github.com/seminar/backend/store"
)

// HydrateFromRedis reads all persisted sessions from Redis and restores them
// into the hub's in-memory map. Call this once at startup before accepting connections.
func HydrateFromRedis(h *Hub, st *store.Store) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ids, err := st.ListSessionIDs(ctx)
	if err != nil {
		slog.Error("failed to list session IDs from Redis", "err", err)
		return
	}

	loaded := 0
	for _, id := range ids {
		meta, exists, err := st.LoadMeta(ctx, id)
		if err != nil {
			slog.Error("LoadMeta failed during hydration", "session", id, "err", err)
			continue
		}
		if !exists {
			continue
		}

		// Load highlights from Redis (stored newest-first via LPUSH).
		redisHighlights, err := st.GetHighlights(ctx, id)
		if err != nil {
			slog.Error("GetHighlights failed during hydration", "session", id, "err", err)
			redisHighlights = nil
		}

		// Reverse so they are oldest-first to match the in-memory buffer convention.
		for i, j := 0, len(redisHighlights)-1; i < j; i, j = i+1, j-1 {
			redisHighlights[i], redisHighlights[j] = redisHighlights[j], redisHighlights[i]
		}
		// Cap to in-memory rolling buffer size.
		if len(redisHighlights) > maxHighlightBuf {
			redisHighlights = redisHighlights[len(redisHighlights)-maxHighlightBuf:]
		}

		sess := h.GetOrCreate(id)
		sess.mu.Lock()
		sess.mode = meta.Mode
		sess.hostID = meta.OwnerUserID
		sess.createdAt = meta.CreatedAt
		sess.highlightBuf = make([]HighlightEntry, len(redisHighlights))
		for i, e := range redisHighlights {
			sess.highlightBuf[i] = HighlightEntry{
				ClientID: e.ClientID,
				Initials: e.Initials,
				Text:     e.Text,
			}
		}
		sess.mu.Unlock()
		loaded++
	}

	if loaded > 0 {
		slog.Info("hydrated sessions from Redis", "count", loaded)
	}
}
