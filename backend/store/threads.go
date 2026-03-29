// Package store — thread persistence for Phase 11.
//
// Redis key schema:
//
//	seminar:thread:{id}           — string: serialized Thread JSON
//	seminar:thread:{id}:replies   — list of serialized Reply JSON (LPUSH newest-first, LTRIM to 200)
//	seminar:session:{id}:threads  — sorted set of threadIds, scored by createdAt unix millis
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const maxReplies = 200

// Thread is a question anchored to a specific text passage in the document.
type Thread struct {
	ID          string    `json:"id"`
	SessionID   string    `json:"sessionId"`
	AnchorText  string    `json:"anchorText"`
	AnchorRange string    `json:"anchorRange"` // serialized RangeData JSON from the extension
	AuthorID    string    `json:"authorId"`
	Question    string    `json:"question"`
	CreatedAt   time.Time `json:"createdAt"`
}

// Reply is a response to a Thread — from a human participant or the AI.
type Reply struct {
	ID        string    `json:"id"`
	ThreadID  string    `json:"threadId"`
	AuthorID  string    `json:"authorId"`
	Content   string    `json:"content"`
	IsAI      bool      `json:"isAI"`
	CreatedAt time.Time `json:"createdAt"`
}

// ThreadWithReplies is a Thread bundled with its replies sorted oldest-first.
type ThreadWithReplies struct {
	Thread
	Replies []Reply `json:"replies"`
}

func threadKey(id string) string          { return "seminar:thread:" + id }
func threadRepliesKey(id string) string   { return "seminar:thread:" + id + ":replies" }
func sessionThreadsKey(sid string) string { return "seminar:session:" + sid + ":threads" }

// SaveThread persists a Thread to Redis and registers it in the session sorted set.
func (s *Store) SaveThread(ctx context.Context, t Thread) error {
	data, err := json.Marshal(t)
	if err != nil {
		return fmt.Errorf("marshal thread: %w", err)
	}
	pipe := s.rdb.Pipeline()
	pipe.Set(ctx, threadKey(t.ID), data, SessionTTL)
	pipe.ZAdd(ctx, sessionThreadsKey(t.SessionID), redis.Z{
		Score:  float64(t.CreatedAt.UnixMilli()),
		Member: t.ID,
	})
	pipe.Expire(ctx, sessionThreadsKey(t.SessionID), SessionTTL)
	_, err = pipe.Exec(ctx)
	return err
}

// GetThread loads a single Thread by ID.
// Returns redis.Nil if the thread does not exist.
func (s *Store) GetThread(ctx context.Context, threadID string) (Thread, error) {
	val, err := s.rdb.Get(ctx, threadKey(threadID)).Result()
	if errors.Is(err, redis.Nil) {
		return Thread{}, redis.Nil
	}
	if err != nil {
		return Thread{}, fmt.Errorf("get thread: %w", err)
	}
	var t Thread
	if err := json.Unmarshal([]byte(val), &t); err != nil {
		return Thread{}, fmt.Errorf("unmarshal thread: %w", err)
	}
	return t, nil
}

// AddReply pushes a Reply to the thread's list (newest at index 0),
// caps at maxReplies, and slides the TTL.
func (s *Store) AddReply(ctx context.Context, reply Reply) error {
	data, err := json.Marshal(reply)
	if err != nil {
		return fmt.Errorf("marshal reply: %w", err)
	}
	key := threadRepliesKey(reply.ThreadID)
	pipe := s.rdb.Pipeline()
	pipe.LPush(ctx, key, string(data))
	pipe.LTrim(ctx, key, 0, maxReplies-1)
	pipe.Expire(ctx, key, SessionTTL)
	_, err = pipe.Exec(ctx)
	return err
}

// GetReplies returns all replies for a thread sorted oldest-first.
// Redis list is newest-first (LPUSH); this reverses on read.
func (s *Store) GetReplies(ctx context.Context, threadID string) ([]Reply, error) {
	vals, err := s.rdb.LRange(ctx, threadRepliesKey(threadID), 0, -1).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("get replies: %w", err)
	}
	replies := make([]Reply, 0, len(vals))
	for _, v := range vals {
		var r Reply
		if json.Unmarshal([]byte(v), &r) == nil {
			replies = append(replies, r)
		}
	}
	// Reverse: LPUSH is newest-first; callers expect oldest-first.
	for i, j := 0, len(replies)-1; i < j; i, j = i+1, j-1 {
		replies[i], replies[j] = replies[j], replies[i]
	}
	return replies, nil
}

// GetSessionThreads returns all threads for a session with their replies, sorted oldest-first.
func (s *Store) GetSessionThreads(ctx context.Context, sessionID string) ([]ThreadWithReplies, error) {
	ids, err := s.rdb.ZRange(ctx, sessionThreadsKey(sessionID), 0, -1).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("get session thread ids: %w", err)
	}
	result := make([]ThreadWithReplies, 0, len(ids))
	for _, id := range ids {
		t, err := s.GetThread(ctx, id)
		if errors.Is(err, redis.Nil) {
			continue // stale sorted-set entry — thread expired
		}
		if err != nil {
			return nil, err
		}
		replies, err := s.GetReplies(ctx, id)
		if err != nil {
			return nil, err
		}
		result = append(result, ThreadWithReplies{Thread: t, Replies: replies})
	}
	return result, nil
}
