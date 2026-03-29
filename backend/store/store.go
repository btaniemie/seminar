// Package store owns all Redis persistence for Seminar sessions.
//
// Redis key schema:
//
//	seminar:session:{id}            — hash: mode, createdAt, updatedAt, ownerUserId
//	seminar:session:{id}:highlights — list of serialized HighlightEntry JSON, capped at 100
//	seminar:session:{id}:chat       — list of serialized ChatMessage JSON, capped at 200
package store

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	SessionTTL      = 30 * 24 * time.Hour
	MaxHighlights   = 100
	MaxChatMessages = 200
)

func sessionKey(id string) string      { return "seminar:session:" + id }
func highlightsKey(id string) string   { return "seminar:session:" + id + ":highlights" }
func chatKey(id string) string         { return "seminar:session:" + id + ":chat" }
func divergenceKey(id string) string   { return "seminar:session:" + id + ":divergence_checked" }

// SessionMeta holds the scalar fields stored in the session hash.
type SessionMeta struct {
	Mode        string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	OwnerUserID string
}

// HighlightEntry is a single text selection stored in Redis.
type HighlightEntry struct {
	ClientID string `json:"clientId"`
	Initials string `json:"initials"`
	Text     string `json:"text"`
}

// ChatMessage is a single persisted chat message.
type ChatMessage struct {
	ClientID string `json:"clientId"`
	Role     string `json:"role"` // "user" | "assistant"
	Content  string `json:"content"`
	SentAt   string `json:"sentAt"` // RFC3339
}

// Store wraps a Redis client and owns all session persistence operations.
type Store struct {
	rdb *redis.Client
}

// New creates a Store from the REDIS_URL env var, defaulting to redis://localhost:6379.
func New() *Store {
	url := os.Getenv("REDIS_URL")
	if url == "" {
		url = "redis://localhost:6379"
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		slog.Error("failed to parse REDIS_URL, falling back to localhost:6379", "err", err)
		opts = &redis.Options{Addr: "localhost:6379"}
	}
	rdb := redis.NewClient(opts)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		slog.Error("redis ping failed — persistence unavailable", "err", err)
	} else {
		slog.Info("redis connected", "addr", opts.Addr)
	}
	return &Store{rdb: rdb}
}

// SaveMeta writes session scalar fields to the Redis hash and refreshes TTL.
func (s *Store) SaveMeta(ctx context.Context, id string, meta SessionMeta) error {
	key := sessionKey(id)
	pipe := s.rdb.Pipeline()
	pipe.HSet(ctx, key, map[string]any{
		"mode":        meta.Mode,
		"createdAt":   meta.CreatedAt.UTC().Format(time.RFC3339),
		"updatedAt":   meta.UpdatedAt.UTC().Format(time.RFC3339),
		"ownerUserId": meta.OwnerUserID,
	})
	pipe.Expire(ctx, key, SessionTTL)
	_, err := pipe.Exec(ctx)
	return err
}

// LoadMeta reads session scalar fields from the Redis hash.
// Returns (meta, true, nil) if the session exists, (zero, false, nil) if not.
func (s *Store) LoadMeta(ctx context.Context, id string) (SessionMeta, bool, error) {
	vals, err := s.rdb.HGetAll(ctx, sessionKey(id)).Result()
	if err != nil {
		// Distinguish redis.Nil from a real connection error.
		if errors.Is(err, redis.Nil) {
			return SessionMeta{}, false, nil
		}
		return SessionMeta{}, false, err
	}
	if len(vals) == 0 {
		return SessionMeta{}, false, nil
	}
	meta := SessionMeta{
		Mode:        vals["mode"],
		OwnerUserID: vals["ownerUserId"],
	}
	if v := vals["createdAt"]; v != "" {
		meta.CreatedAt, _ = time.Parse(time.RFC3339, v)
	}
	if v := vals["updatedAt"]; v != "" {
		meta.UpdatedAt, _ = time.Parse(time.RFC3339, v)
	}
	return meta, true, nil
}

// PushHighlight appends a highlight to the session list (newest at index 0),
// caps at MaxHighlights, and slides the TTL.
func (s *Store) PushHighlight(ctx context.Context, id string, entry HighlightEntry) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	key := highlightsKey(id)
	pipe := s.rdb.Pipeline()
	pipe.LPush(ctx, key, string(data))
	pipe.LTrim(ctx, key, 0, MaxHighlights-1)
	pipe.Expire(ctx, key, SessionTTL)
	_, err = pipe.Exec(ctx)
	return err
}

// GetHighlights returns up to MaxHighlights highlights (newest first).
func (s *Store) GetHighlights(ctx context.Context, id string) ([]HighlightEntry, error) {
	vals, err := s.rdb.LRange(ctx, highlightsKey(id), 0, MaxHighlights-1).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, err
	}
	entries := make([]HighlightEntry, 0, len(vals))
	for _, v := range vals {
		var e HighlightEntry
		if json.Unmarshal([]byte(v), &e) == nil {
			entries = append(entries, e)
		}
	}
	return entries, nil
}

// HighlightCount returns the number of stored highlights for a session.
func (s *Store) HighlightCount(ctx context.Context, id string) (int64, error) {
	n, err := s.rdb.LLen(ctx, highlightsKey(id)).Result()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	return n, err
}

// PushChatMessage appends a chat message (newest at index 0),
// caps at MaxChatMessages, and slides the TTL.
func (s *Store) PushChatMessage(ctx context.Context, id string, msg ChatMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	key := chatKey(id)
	pipe := s.rdb.Pipeline()
	pipe.LPush(ctx, key, string(data))
	pipe.LTrim(ctx, key, 0, MaxChatMessages-1)
	pipe.Expire(ctx, key, SessionTTL)
	_, err = pipe.Exec(ctx)
	return err
}

// ChatCount returns the number of stored chat messages for a session.
func (s *Store) ChatCount(ctx context.Context, id string) (int64, error) {
	n, err := s.rdb.LLen(ctx, chatKey(id)).Result()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	return n, err
}

// SessionSummary combines metadata and list counts for the GET /api/session/{id} response.
type SessionSummary struct {
	Meta           SessionMeta
	HighlightCount int64
	ChatCount      int64
}

// GetSessionSummary fetches session metadata and list counts in a single pipeline round-trip.
// Returns (zero, false, nil) if the session does not exist in Redis.
func (s *Store) GetSessionSummary(ctx context.Context, id string) (SessionSummary, bool, error) {
	pipe := s.rdb.Pipeline()
	hashCmd := pipe.HGetAll(ctx, sessionKey(id))
	hCmd := pipe.LLen(ctx, highlightsKey(id))
	cCmd := pipe.LLen(ctx, chatKey(id))
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return SessionSummary{}, false, err
	}

	vals, err := hashCmd.Result()
	if err != nil || len(vals) == 0 {
		return SessionSummary{}, false, nil
	}

	meta := SessionMeta{
		Mode:        vals["mode"],
		OwnerUserID: vals["ownerUserId"],
	}
	if v := vals["createdAt"]; v != "" {
		meta.CreatedAt, _ = time.Parse(time.RFC3339, v)
	}
	if v := vals["updatedAt"]; v != "" {
		meta.UpdatedAt, _ = time.Parse(time.RFC3339, v)
	}

	hCount, _ := hCmd.Result()
	cCount, _ := cCmd.Result()
	return SessionSummary{Meta: meta, HighlightCount: hCount, ChatCount: cCount}, true, nil
}

// GetChatHistory returns up to MaxChatMessages messages (newest first).
func (s *Store) GetChatHistory(ctx context.Context, id string) ([]ChatMessage, error) {
	vals, err := s.rdb.LRange(ctx, chatKey(id), 0, MaxChatMessages-1).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, err
	}
	msgs := make([]ChatMessage, 0, len(vals))
	for _, v := range vals {
		var m ChatMessage
		if json.Unmarshal([]byte(v), &m) == nil {
			msgs = append(msgs, m)
		}
	}
	return msgs, nil
}

// SaveComprehension stores a comprehension map JSON string for a session.
func (s *Store) SaveComprehension(ctx context.Context, sessionID, jsonStr string) error {
	key := "seminar:session:" + sessionID + ":comprehension"
	pipe := s.rdb.Pipeline()
	pipe.Set(ctx, key, jsonStr, SessionTTL)
	_, err := pipe.Exec(ctx)
	return err
}

// IsDivergencePairChecked reports whether a highlight pair has already been processed.
// pairKey should be the two clientIDs joined with ":", sorted lexicographically.
func (s *Store) IsDivergencePairChecked(ctx context.Context, sessionID, pairKey string) (bool, error) {
	return s.rdb.SIsMember(ctx, divergenceKey(sessionID), pairKey).Result()
}

// MarkDivergencePairChecked records a processed pair so it is never re-triggered.
func (s *Store) MarkDivergencePairChecked(ctx context.Context, sessionID, pairKey string) error {
	key := divergenceKey(sessionID)
	pipe := s.rdb.Pipeline()
	pipe.SAdd(ctx, key, pairKey)
	pipe.Expire(ctx, key, SessionTTL)
	_, err := pipe.Exec(ctx)
	return err
}

// ListSessionIDs returns all session IDs present in Redis.
// Only returns top-level session keys (excludes :highlights and :chat sub-keys).
// Used once at startup to hydrate the in-memory hub.
func (s *Store) ListSessionIDs(ctx context.Context) ([]string, error) {
	keys, err := s.rdb.Keys(ctx, "seminar:session:*").Result()
	if err != nil {
		return nil, err
	}
	const prefix = "seminar:session:"
	var ids []string
	for _, k := range keys {
		rest := k[len(prefix):]
		// Sub-keys contain a colon (e.g. "abc123:highlights"). Session IDs are 8-char hex — no colons.
		if !strings.Contains(rest, ":") {
			ids = append(ids, rest)
		}
	}
	return ids, nil
}
