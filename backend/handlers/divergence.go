package handlers

import (
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

// DivergenceChecker implements hub.DivergenceFunc.
// It checks whether a new highlight overlaps with any existing highlight from a
// different user, and — on first detection — asks Claude to name the tension.
type DivergenceChecker struct {
	apiKey string
	store  *store.Store
	hub    *hub.Hub
}

// NewDivergenceChecker creates a DivergenceChecker.
func NewDivergenceChecker(st *store.Store, h *hub.Hub) *DivergenceChecker {
	return &DivergenceChecker{
		apiKey: os.Getenv("ANTHROPIC_API_KEY"),
		store:  st,
		hub:    h,
	}
}

// Check satisfies hub.DivergenceFunc.
// all is a snapshot of the full highlight buffer; newEntry is the one just added.
func (dc *DivergenceChecker) Check(sessionID string, all []hub.HighlightEntry, newEntry hub.HighlightEntry) {
	if dc.apiKey == "" {
		return
	}

	for _, existing := range all {
		// Only check highlights from different users.
		if existing.ClientID == newEntry.ClientID {
			continue
		}
		// Skip if the texts don't overlap.
		if !highlightsOverlap(existing.Text, newEntry.Text) {
			continue
		}

		// Deterministic pair key — same regardless of order.
		pairKey := sortedPairKey(existing.ClientID, newEntry.ClientID)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		already, err := dc.store.IsDivergencePairChecked(ctx, sessionID, pairKey)
		cancel()
		if err != nil {
			slog.Error("IsDivergencePairChecked failed", "session", sessionID, "err", err)
			continue
		}
		if already {
			continue // already handled — skip
		}

		// Mark before calling Claude so a race can't fire it twice.
		markCtx, markCancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := dc.store.MarkDivergencePairChecked(markCtx, sessionID, pairKey); err != nil {
			slog.Error("MarkDivergencePairChecked failed", "session", sessionID, "err", err)
			markCancel()
			continue
		}
		markCancel()

		// Look up thread questions for each user (best-effort).
		questionA := dc.threadQuestion(sessionID, existing.ClientID)
		questionB := dc.threadQuestion(sessionID, newEntry.ClientID)

		// Determine which passage to surface as the shared anchor.
		passage := sharedPassage(existing.Text, newEntry.Text)

		message, err := dc.callClaude(existing.Text, questionA, newEntry.Text, questionB, passage)
		if err != nil {
			slog.Error("divergence Claude call failed", "session", sessionID, "err", err)
			continue
		}

		dc.broadcast(sessionID, passage, message)
	}
}

// ── Overlap logic ─────────────────────────────────────────────────────────────

func normalizeText(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(s))), " ")
}

func highlightsOverlap(a, b string) bool {
	na, nb := normalizeText(a), normalizeText(b)
	if na == "" || nb == "" {
		return false
	}
	if strings.Contains(na, nb) || strings.Contains(nb, na) {
		return true
	}
	return longestCommonSubstringLen([]rune(na), []rune(nb)) >= 30
}

// longestCommonSubstringLen returns the length (in runes) of the longest common
// substring between two rune slices. O(n*m) — acceptable for short highlights.
func longestCommonSubstringLen(a, b []rune) int {
	max := 0
	dp := make([]int, len(b)+1)
	for i := 1; i <= len(a); i++ {
		prev := 0
		for j := 1; j <= len(b); j++ {
			tmp := dp[j]
			if a[i-1] == b[j-1] {
				dp[j] = prev + 1
				if dp[j] > max {
					max = dp[j]
				}
			} else {
				dp[j] = 0
			}
			prev = tmp
		}
	}
	return max
}

// sharedPassage returns the shorter of the two texts as the "anchor" passage to show.
func sharedPassage(a, b string) string {
	if len(a) <= len(b) {
		return a
	}
	return b
}

// sortedPairKey returns "minID:maxID" for deterministic deduplication.
func sortedPairKey(a, b string) string {
	if a < b {
		return a + ":" + b
	}
	return b + ":" + a
}

// ── Thread question lookup ─────────────────────────────────────────────────────

func (dc *DivergenceChecker) threadQuestion(sessionID, clientID string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	threads, err := dc.store.GetSessionThreads(ctx, sessionID)
	if err != nil {
		return ""
	}
	for _, t := range threads {
		if t.AuthorID == clientID {
			return t.Question
		}
	}
	return ""
}

// ── Claude call ───────────────────────────────────────────────────────────────

func (dc *DivergenceChecker) callClaude(textA, questionA, textB, questionB, passage string) (string, error) {
	userMsg := fmt.Sprintf(
		"Two students in the same reading session highlighted overlapping passages but may be reading them differently.\n\n"+
			"Student A highlighted: \"\"\"%s\"\"\"\n"+
			"Student A's thread question (if any): \"\"\"%s\"\"\"\n\n"+
			"Student B highlighted: \"\"\"%s\"\"\"\n"+
			"Student B's thread question (if any): \"\"\"%s\"\"\"\n\n"+
			"In one sentence, name the interpretive tension between these two readings. "+
			"Then ask one question that invites both students to compare their thinking. "+
			"Do not explain the passage. Format: two sentences, plain language.",
		textA, questionA, textB, questionB,
	)

	body, _ := json.Marshal(map[string]any{
		"model":      "claude-haiku-4-5-20251001",
		"max_tokens": 150,
		"messages":   []map[string]string{{"role": "user", "content": userMsg}},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("x-api-key", dc.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("claude returned %d", resp.StatusCode)
	}

	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || len(result.Content) == 0 {
		return "", fmt.Errorf("bad claude response")
	}
	return result.Content[0].Text, nil
}

// ── Broadcast ─────────────────────────────────────────────────────────────────

func (dc *DivergenceChecker) broadcast(sessionID, passage, message string) {
	sess := dc.hub.Get(sessionID)
	if sess == nil {
		return
	}

	payload, err := json.Marshal(map[string]string{
		"passage": passage,
		"message": message,
	})
	if err != nil {
		slog.Error("divergence marshal payload", "err", err)
		return
	}

	env := hub.Envelope{
		Type:      "divergence",
		SessionID: sessionID,
		Payload:   json.RawMessage(payload),
	}
	data, err := json.Marshal(env)
	if err != nil {
		slog.Error("divergence marshal envelope", "err", err)
		return
	}

	sess.Broadcast(data)
}
