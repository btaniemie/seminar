package handlers

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/seminar/backend/hub"
)

// Base Socratic rules shared across all modes.
const socraticBase = `Rules you must always follow:
- Guide students to insights through questions — never provide answers or explanations directly
- Never summarize, paraphrase, or translate the text for the student
- Never complete tasks, assignments, or essays on the student's behalf
- Ask only one focused question per response and keep it brief
- If a student asks you to explain something directly, respond with a question that helps them figure it out themselves
- Reference specific words or phrases from the highlighted text when relevant
- Build on what the student says — follow their line of reasoning`

const promptDefault = `You are a Socratic reading partner for a collaborative study group.

` + socraticBase

const promptCloseReading = `You are a Socratic partner for a close reading session.

` + socraticBase + `

Close reading focus:
- Slow students down to the level of individual words, phrases, and sentences
- Ask what a specific word choice signals, why the author constructed a sentence this way, what a metaphor is doing
- Surface structural patterns: repetition, contrast, shifts in tone or register
- Push toward the "how" and "why" of the text, not just the "what"`

const promptDebatePrep = `You are a Socratic debate coach.

` + socraticBase + `

Debate prep focus:
- Help students steel-man every position, including ones they disagree with
- Surface hidden assumptions in their arguments — ask "what has to be true for that claim to hold?"
- Push them to anticipate the strongest counterargument before they've made it
- Probe the quality of evidence: is it sufficient? representative? could it support the opposing view?
- Never take a side yourself`

const promptExamReview = `You are a Socratic exam tutor.

` + socraticBase + `

Exam review focus:
- Ask one focused exam-style question per response about the highlighted text or the student's claims
- If they answer correctly, probe deeper or introduce a harder variant
- If they struggle, give a single hint that points at the right frame — never give the answer
- Vary question types: identification, comparison, causation, significance, critique`

// ChatHandler handles POST /api/chat.
// It calls the Anthropic API with a Socratic system prompt and streams
// text deltas back to the caller as Server-Sent Events.
type ChatHandler struct {
	apiKey string
	hub    *hub.Hub
}

func NewChatHandler(h *hub.Hub) *ChatHandler {
	return &ChatHandler{apiKey: os.Getenv("ANTHROPIC_API_KEY"), hub: h}
}

type chatRequest struct {
	SessionID string       `json:"sessionId"`
	Messages  []apiMessage `json:"messages"`
	Context   readContext  `json:"context"`
}

type apiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type readContext struct {
	Highlight string `json:"highlight"`
	PageTitle string `json:"pageTitle"`
	PageURL   string `json:"pageUrl"`
}

func (ch *ChatHandler) Chat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if ch.apiKey == "" {
		http.Error(w, "ANTHROPIC_API_KEY not configured", http.StatusInternalServerError)
		return
	}

	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if len(req.Messages) == 0 {
		http.Error(w, "messages required", http.StatusBadRequest)
		return
	}

	// Fetch session state (mode + highlight buffer) — best-effort, nil-safe.
	var sessionHighlights []hub.HighlightEntry
	sessionMode := ""
	if req.SessionID != "" {
		if s := ch.hub.Get(req.SessionID); s != nil {
			sessionHighlights = s.GetHighlights()
			sessionMode = s.GetMode()
		}
	}

	system := buildSystemPrompt(req.Context, sessionHighlights, sessionMode)

	anthropicBody, err := json.Marshal(map[string]any{
		"model":      "claude-sonnet-4-20250514",
		"max_tokens": 1024,
		"stream":     true,
		"system":     system,
		"messages":   req.Messages,
	})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Pass the request context so the upstream call is cancelled if the
	// browser tab closes mid-stream.
	apiReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
		"https://api.anthropic.com/v1/messages", bytes.NewReader(anthropicBody))
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	apiReq.Header.Set("x-api-key", ch.apiKey)
	apiReq.Header.Set("anthropic-version", "2023-06-01")
	apiReq.Header.Set("content-type", "application/json")

	apiResp, err := http.DefaultClient.Do(apiReq)
	if err != nil {
		log.Printf("[chat] anthropic request failed: %v", err)
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}
	defer apiResp.Body.Close()

	if apiResp.StatusCode != http.StatusOK {
		log.Printf("[chat] anthropic returned %d", apiResp.StatusCode)
		http.Error(w, "upstream error", apiResp.StatusCode)
		return
	}

	// ── Stream response to client as SSE ──────────────────────────────────
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx buffering if present
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

	scanner := bufio.NewScanner(apiResp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")

		var event deltaEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		if event.Type == "content_block_delta" && event.Delta.Type == "text_delta" {
			chunk, _ := json.Marshal(map[string]string{"text": event.Delta.Text})
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			flusher.Flush()
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("[chat] stream read error: %v", err)
	}

	fmt.Fprintf(w, "data: {\"done\":true}\n\n")
	flusher.Flush()
}

func systemPromptForMode(mode string) string {
	switch mode {
	case "close-reading":
		return promptCloseReading
	case "debate-prep":
		return promptDebatePrep
	case "exam-review":
		return promptExamReview
	default:
		return promptDefault
	}
}

func buildSystemPrompt(ctx readContext, highlights []hub.HighlightEntry, mode string) string {
	s := systemPromptForMode(mode)
	if ctx.PageTitle != "" || ctx.PageURL != "" {
		s += fmt.Sprintf("\n\nReading session context:\n- Page: %s (%s)", ctx.PageTitle, ctx.PageURL)
	}
	if ctx.Highlight != "" {
		s += fmt.Sprintf("\n- Currently highlighted text: \"%s\"", ctx.Highlight)
	}
	if len(highlights) > 0 {
		s += "\n\nRecent highlights from all session participants (oldest → newest):"
		for _, h := range highlights {
			s += fmt.Sprintf("\n- [%s] \"%s\"", h.Initials, h.Text)
		}
		s += "\n\nUse these highlights to deepen your Socratic questioning: notice if multiple people selected the same passage (ask what each sees differently), spot patterns across selections, or ask a participant why they flagged something their partner skipped."
	}
	return s
}
