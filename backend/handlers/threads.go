package handlers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/seminar/backend/hub"
	"github.com/seminar/backend/store"
)

// ThreadHandler handles all /api/threads/* endpoints.
type ThreadHandler struct {
	apiKey string
	hub    *hub.Hub
	store  *store.Store
}

// NewThreadHandler creates a ThreadHandler. Reads ANTHROPIC_API_KEY from env.
func NewThreadHandler(h *hub.Hub, st *store.Store) *ThreadHandler {
	return &ThreadHandler{
		apiKey: os.Getenv("ANTHROPIC_API_KEY"),
		hub:    h,
		store:  st,
	}
}

// ── Request types ─────────────────────────────────────────────────────────────

type createThreadRequest struct {
	SessionID   string `json:"sessionId"`
	AnchorText  string `json:"anchorText"`
	AnchorRange string `json:"anchorRange"` // serialized RangeData JSON
	AuthorID    string `json:"authorId"`
	Question    string `json:"question"`
}

type addReplyRequest struct {
	AuthorID string `json:"authorId"`
	Content  string `json:"content"`
}

type askRequest struct {
	PageTitle string `json:"pageTitle"`
	URL       string `json:"url"`
}

// ── Broadcast helpers ─────────────────────────────────────────────────────────

func (th *ThreadHandler) broadcast(sessionID, msgType string, twr store.ThreadWithReplies) {
	payload, err := json.Marshal(twr)
	if err != nil {
		slog.Error("marshal thread broadcast payload", "type", msgType, "err", err)
		return
	}
	env := hub.Envelope{
		Type:      msgType,
		SessionID: sessionID,
		Payload:   json.RawMessage(payload),
	}
	data, err := json.Marshal(env)
	if err != nil {
		slog.Error("marshal thread broadcast envelope", "type", msgType, "err", err)
		return
	}
	if sess := th.hub.Get(sessionID); sess != nil {
		sess.Broadcast(data)
	}
}

// ── Handlers ──────────────────────────────────────────────────────────────────

// Create handles POST /api/threads.
// Body: { sessionId, anchorText, anchorRange, authorId, question }
// Returns the new ThreadWithReplies and broadcasts thread_new to all session clients.
func (th *ThreadHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req createThreadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if req.SessionID == "" || req.AnchorText == "" || req.Question == "" {
		http.Error(w, "sessionId, anchorText, and question are required", http.StatusBadRequest)
		return
	}

	id, err := generateSessionID() // reuse — same 8-char hex logic
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	t := store.Thread{
		ID:          id,
		SessionID:   req.SessionID,
		AnchorText:  req.AnchorText,
		AnchorRange: req.AnchorRange,
		AuthorID:    req.AuthorID,
		Question:    req.Question,
		CreatedAt:   time.Now().UTC(),
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	if err := th.store.SaveThread(ctx, t); err != nil {
		slog.Error("SaveThread failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	twr := store.ThreadWithReplies{Thread: t, Replies: []store.Reply{}}
	th.broadcast(req.SessionID, "thread_new", twr)

	writeJSON(w, http.StatusCreated, twr)
}

// AddReply handles POST /api/threads/{id}/reply.
// Body: { authorId, content }
// Returns the updated ThreadWithReplies and broadcasts thread_update.
func (th *ThreadHandler) AddReply(w http.ResponseWriter, r *http.Request) {
	threadID := r.PathValue("id")

	var req addReplyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if req.Content == "" {
		http.Error(w, "content required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	t, err := th.store.GetThread(ctx, threadID)
	if errors.Is(err, redis.Nil) {
		http.Error(w, "thread not found", http.StatusNotFound)
		return
	}
	if err != nil {
		slog.Error("GetThread failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	replyID, err := generateSessionID()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	reply := store.Reply{
		ID:        replyID,
		ThreadID:  threadID,
		AuthorID:  req.AuthorID,
		Content:   req.Content,
		IsAI:      false,
		CreatedAt: time.Now().UTC(),
	}
	if err := th.store.AddReply(ctx, reply); err != nil {
		slog.Error("AddReply failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	replies, err := th.store.GetReplies(ctx, threadID)
	if err != nil {
		slog.Error("GetReplies failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	twr := store.ThreadWithReplies{Thread: t, Replies: replies}
	th.broadcast(t.SessionID, "thread_update", twr)
	writeJSON(w, http.StatusOK, twr)
}

// AskAI handles POST /api/threads/{id}/ask.
// Body: { pageTitle, url }
// Calls Claude with thread context, streams via SSE to the requesting client,
// then saves the completed reply to Redis and broadcasts thread_update to the session.
// Partial streamed text is never written to Redis — only the completed reply is.
func (th *ThreadHandler) AskAI(w http.ResponseWriter, r *http.Request) {
	if th.apiKey == "" {
		http.Error(w, "ANTHROPIC_API_KEY not configured", http.StatusInternalServerError)
		return
	}

	threadID := r.PathValue("id")

	var req askRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Use a background context for the full operation so Redis writes
	// succeed even after the SSE response has been flushed.
	bgCtx, bgCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer bgCancel()

	t, err := th.store.GetThread(bgCtx, threadID)
	if errors.Is(err, redis.Nil) {
		http.Error(w, "thread not found", http.StatusNotFound)
		return
	}
	if err != nil {
		slog.Error("GetThread (ask) failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	replies, err := th.store.GetReplies(bgCtx, threadID)
	if err != nil {
		slog.Error("GetReplies (ask) failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	mode := ""
	if sess := th.hub.Get(t.SessionID); sess != nil {
		mode = sess.GetMode()
	}

	system := buildThreadSystemPrompt(t, replies, req.PageTitle, req.URL, mode)

	body, err := json.Marshal(map[string]any{
		"model":      "claude-sonnet-4-20250514",
		"max_tokens": 512,
		"stream":     true,
		"system":     system,
		"messages":   []map[string]string{{"role": "user", "content": t.Question}},
	})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Use the request context for the upstream Claude call so it is cancelled
	// if the browser tab closes mid-stream.
	apiReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
		"https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	apiReq.Header.Set("x-api-key", th.apiKey)
	apiReq.Header.Set("anthropic-version", "2023-06-01")
	apiReq.Header.Set("content-type", "application/json")

	apiResp, err := http.DefaultClient.Do(apiReq)
	if err != nil {
		slog.Error("anthropic thread ask failed", "err", err)
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}
	defer apiResp.Body.Close()

	if apiResp.StatusCode != http.StatusOK {
		slog.Error("anthropic thread ask non-200", "status", apiResp.StatusCode)
		http.Error(w, "upstream error", apiResp.StatusCode)
		return
	}

	// ── Stream SSE to caller, buffering full text ─────────────────────────────
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	type deltaEvent struct {
		Type  string `json:"type"`
		Delta struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"delta"`
	}

	var fullText strings.Builder
	scanner := bufio.NewScanner(apiResp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		raw := strings.TrimPrefix(line, "data: ")
		var event deltaEvent
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			continue
		}
		if event.Type == "content_block_delta" && event.Delta.Type == "text_delta" {
			fullText.WriteString(event.Delta.Text)
			chunk, _ := json.Marshal(map[string]string{"text": event.Delta.Text})
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			flusher.Flush()
		}
	}
	if err := scanner.Err(); err != nil {
		slog.Error("thread ask stream scan error", "err", err)
	}
	fmt.Fprintf(w, "data: {\"done\":true}\n\n")
	flusher.Flush()

	// ── Persist completed reply + broadcast ───────────────────────────────────
	completed := fullText.String()
	if completed == "" {
		return
	}

	replyID, err := generateSessionID()
	if err != nil {
		slog.Error("generateSessionID for AI reply", "err", err)
		return
	}

	aiReply := store.Reply{
		ID:        replyID,
		ThreadID:  threadID,
		AuthorID:  "ai",
		Content:   completed,
		IsAI:      true,
		CreatedAt: time.Now().UTC(),
	}

	saveCtx, saveCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer saveCancel()

	if err := th.store.AddReply(saveCtx, aiReply); err != nil {
		slog.Error("AddReply (AI) failed", "err", err)
		return
	}

	allReplies, err := th.store.GetReplies(saveCtx, threadID)
	if err != nil {
		slog.Error("GetReplies after AI reply failed", "err", err)
		return
	}

	twr := store.ThreadWithReplies{Thread: t, Replies: allReplies}
	th.broadcast(t.SessionID, "thread_update", twr)
}

// GetBySession handles GET /api/threads/{sessionId}.
// Returns all threads for the session with their replies, sorted oldest-first.
func (th *ThreadHandler) GetBySession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("sessionId")

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	threads, err := th.store.GetSessionThreads(ctx, sessionID)
	if err != nil {
		slog.Error("GetSessionThreads failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if threads == nil {
		threads = []store.ThreadWithReplies{}
	}
	writeJSON(w, http.StatusOK, threads)
}

// ── Claude prompt ─────────────────────────────────────────────────────────────

func buildThreadSystemPrompt(t store.Thread, replies []store.Reply, pageTitle, url, mode string) string {
	var sb strings.Builder
	sb.WriteString("You are a Socratic reading partner responding to a specific question a student anchored to a passage.\n\n")
	sb.WriteString(fmt.Sprintf("Passage they highlighted: \"\"\"%s\"\"\"\n", t.AnchorText))
	sb.WriteString(fmt.Sprintf("Their question: \"\"\"%s\"\"\"\n", t.Question))

	if len(replies) > 0 {
		sb.WriteString("\nPrior replies in this thread:\n")
		for _, r := range replies {
			who := "Student"
			if r.IsAI {
				who = "Seminar"
			}
			sb.WriteString(fmt.Sprintf("- %s: %s\n", who, r.Content))
		}
	}

	if pageTitle != "" || url != "" {
		sb.WriteString(fmt.Sprintf("\nPage: %s (%s)\n", pageTitle, url))
	}
	if mode != "" {
		sb.WriteString(fmt.Sprintf("Session mode: %s\n", mode))
	}

	sb.WriteString("\nRespond as a Socratic partner — do not answer directly. Ask a question that advances their thinking about this specific passage. Keep it to 2–3 sentences. Do not summarize the passage.")
	return sb.String()
}
