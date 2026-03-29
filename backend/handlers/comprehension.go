package handlers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/seminar/backend/hub"
	"github.com/seminar/backend/store"
)

// ComprehensionHandler handles POST /api/comprehension.
type ComprehensionHandler struct {
	apiKey string
	store  *store.Store
	hub    *hub.Hub
}

// NewComprehensionHandler creates a ComprehensionHandler.
func NewComprehensionHandler(st *store.Store, h *hub.Hub) *ComprehensionHandler {
	return &ComprehensionHandler{
		apiKey: os.Getenv("ANTHROPIC_API_KEY"),
		store:  st,
		hub:    h,
	}
}

// ── Request types ─────────────────────────────────────────────────────────────

type comprehensionRequest struct {
	SessionID   string        `json:"sessionId"`
	PageTitle   string        `json:"pageTitle"`
	URL         string        `json:"url"`
	Mode        string        `json:"mode"`
	Highlights  []comprHL     `json:"highlights"`
	Threads     []comprThread `json:"threads"`
	ChatHistory []comprChat   `json:"chatHistory"`
}

type comprHL struct {
	ClientID string `json:"clientId"`
	Initials string `json:"initials"`
	Text     string `json:"text"`
}

type comprThread struct {
	AnchorText string      `json:"anchorText"`
	Question   string      `json:"question"`
	AuthorID   string      `json:"authorId"`
	Replies    []comprReply `json:"replies"`
}

type comprReply struct {
	AuthorID string `json:"authorId"`
	Content  string `json:"content"`
	IsAI     bool   `json:"isAI"`
}

type comprChat struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// comprehensionMap is what Claude returns and what we store + broadcast.
type comprehensionMap struct {
	Understood          []string `json:"understood"`
	Friction            []string `json:"friction"`
	Unresolved          []string `json:"unresolved"`
	RecommendedFollowup []string `json:"recommended_followup"`
}

// ── Handler ───────────────────────────────────────────────────────────────────

// Comprehension handles POST /api/comprehension.
// Streams Claude's JSON response token-by-token via SSE, validates the complete
// JSON on stream end, saves to Redis, then broadcasts session_ended to all peers.
func (ch *ComprehensionHandler) Comprehension(w http.ResponseWriter, r *http.Request) {
	if ch.apiKey == "" {
		http.Error(w, "ANTHROPIC_API_KEY not configured", http.StatusInternalServerError)
		return
	}

	var req comprehensionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.SessionID == "" {
		http.Error(w, "sessionId required", http.StatusBadRequest)
		return
	}

	system := buildComprehensionPrompt(req)

	body, err := json.Marshal(map[string]any{
		"model":      "claude-sonnet-4-6",
		"max_tokens": 700,
		"stream":     true,
		"system":     "You are analyzing a college study group reading session. Return ONLY a valid JSON object — no preamble, no markdown fences, no commentary. The JSON must contain exactly these four keys: \"understood\", \"friction\", \"unresolved\", \"recommended_followup\". Each value is an array of plain-language strings a college student would use.",
		"messages":   []map[string]string{{"role": "user", "content": system}},
	})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	apiReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
		"https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	apiReq.Header.Set("x-api-key", ch.apiKey)
	apiReq.Header.Set("anthropic-version", "2023-06-01")
	apiReq.Header.Set("content-type", "application/json")

	apiResp, err := http.DefaultClient.Do(apiReq)
	if err != nil {
		slog.Error("comprehension claude call failed", "err", err)
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}
	defer apiResp.Body.Close()

	if apiResp.StatusCode != http.StatusOK {
		slog.Error("comprehension claude non-200", "status", apiResp.StatusCode)
		http.Error(w, "upstream error", apiResp.StatusCode)
		return
	}

	// ── SSE stream to client ──────────────────────────────────────────────────
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
		slog.Error("comprehension stream scan error", "err", err)
	}

	// ── Validate JSON ─────────────────────────────────────────────────────────
	rawJSON := strings.TrimSpace(fullText.String())
	// Claude sometimes wraps JSON in markdown code fences despite instructions — strip them.
	rawJSON = strings.TrimPrefix(rawJSON, "```json")
	rawJSON = strings.TrimPrefix(rawJSON, "```")
	rawJSON = strings.TrimSuffix(rawJSON, "```")
	rawJSON = strings.TrimSpace(rawJSON)

	var result comprehensionMap
	if err := json.Unmarshal([]byte(rawJSON), &result); err != nil {
		slog.Error("comprehension: invalid JSON from Claude", "err", err, "raw", rawJSON)
		fmt.Fprintf(w, "data: {\"error\":\"invalid response from AI\"}\n\n")
		flusher.Flush()
		return
	}
	// Ensure all required keys are present.
	if len(result.Understood) == 0 && len(result.Friction) == 0 &&
		len(result.Unresolved) == 0 && len(result.RecommendedFollowup) == 0 {
		slog.Error("comprehension: all arrays empty", "raw", rawJSON)
		fmt.Fprintf(w, "data: {\"error\":\"empty response from AI\"}\n\n")
		flusher.Flush()
		return
	}

	// ── Persist + broadcast ───────────────────────────────────────────────────
	bgCtx, bgCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer bgCancel()

	if err := ch.store.SaveComprehension(bgCtx, req.SessionID, rawJSON); err != nil {
		slog.Error("SaveComprehension failed", "session", req.SessionID, "err", err)
	}

	ch.broadcastSessionEnded(req.SessionID, result)

	// ── Final SSE event ───────────────────────────────────────────────────────
	donePayload, _ := json.Marshal(map[string]any{
		"done":          true,
		"comprehension": result,
	})
	fmt.Fprintf(w, "data: %s\n\n", donePayload)
	flusher.Flush()
}

// broadcastSessionEnded fans out { type: "session_ended", payload: { comprehension } }.
func (ch *ComprehensionHandler) broadcastSessionEnded(sessionID string, result comprehensionMap) {
	sess := ch.hub.Get(sessionID)
	if sess == nil {
		return
	}
	payload, err := json.Marshal(map[string]any{"comprehension": result})
	if err != nil {
		slog.Error("comprehension broadcast marshal", "err", err)
		return
	}
	env := hub.Envelope{
		Type:      "session_ended",
		SessionID: sessionID,
		Payload:   json.RawMessage(payload),
	}
	data, err := json.Marshal(env)
	if err != nil {
		slog.Error("comprehension broadcast envelope marshal", "err", err)
		return
	}
	sess.Broadcast(data)
}

// ── Prompt builder ────────────────────────────────────────────────────────────

func buildComprehensionPrompt(req comprehensionRequest) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Page: %s (%s)\n", req.PageTitle, req.URL))
	if req.Mode != "" {
		sb.WriteString(fmt.Sprintf("Mode: %s\n", req.Mode))
	}
	sb.WriteString("\n")

	if len(req.Highlights) > 0 {
		sb.WriteString("Highlights (all users):\n")
		for _, h := range req.Highlights {
			who := h.Initials
			if who == "" {
				who = h.ClientID
			}
			sb.WriteString(fmt.Sprintf("- [%s]: %s\n", who, h.Text))
		}
		sb.WriteString("\n")
	}

	if len(req.Threads) > 0 {
		sb.WriteString("Threads and replies:\n")
		for _, t := range req.Threads {
			sb.WriteString(fmt.Sprintf("Q: %s anchored to '%s'\n", t.Question, t.AnchorText))
			if len(t.Replies) > 0 {
				sb.WriteString("Replies:\n")
				for _, r := range t.Replies {
					who := "Student"
					if r.IsAI {
						who = "Seminar"
					}
					sb.WriteString(fmt.Sprintf("  - %s: %s\n", who, r.Content))
				}
			}
		}
		sb.WriteString("\n")
	}

	if len(req.ChatHistory) > 0 {
		sb.WriteString("Chat history:\n")
		for _, m := range req.ChatHistory {
			role := m.Role
			if role == "assistant" {
				role = "Seminar"
			} else {
				role = "Student"
			}
			sb.WriteString(fmt.Sprintf("%s: %s\n", role, m.Content))
		}
		sb.WriteString("\n")
	}

	sb.WriteString(`Analyze the group's thinking — not the text itself. Return ONLY a valid JSON object:
{
  "understood": ["2–4 concepts or passages the group engaged with confidently"],
  "friction": ["2–4 passages or concepts that generated the most questions or disagreement"],
  "unresolved": ["2–4 questions that were asked but never satisfactorily answered"],
  "recommended_followup": ["1–2 specific things worth returning to before an exam or discussion"]
}

Rules:
- Plain language a college student would use, not academic jargon
- "understood" = passages where the group asked confident follow-up questions
- "friction" = passages with multiple highlights, disagreements, or repeated questions
- "unresolved" = questions posed but dropped without resolution
- Return nothing except the JSON object`)

	return sb.String()
}
