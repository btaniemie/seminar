package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// BriefingHandler handles POST /api/briefing.
type BriefingHandler struct {
	apiKey string
}

// NewBriefingHandler creates a BriefingHandler. Reads ANTHROPIC_API_KEY from env.
func NewBriefingHandler() *BriefingHandler {
	return &BriefingHandler{apiKey: os.Getenv("ANTHROPIC_API_KEY")}
}

type briefingRequest struct {
	URL   string `json:"url"`
	Title string `json:"title"`
}

// Briefing handles POST /api/briefing.
// Fetches the page at the given URL, extracts visible text, and asks Claude Haiku
// for a 3-sentence reading brief. Returns { briefing: string }.
func (bh *BriefingHandler) Briefing(w http.ResponseWriter, r *http.Request) {
	if bh.apiKey == "" {
		http.Error(w, "ANTHROPIC_API_KEY not configured", http.StatusInternalServerError)
		return
	}

	var req briefingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.URL == "" {
		http.Error(w, "url required", http.StatusBadRequest)
		return
	}

	// Fetch the page (10 s timeout so we don't block forever on slow pages).
	fetchCtx, fetchCancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer fetchCancel()

	pageReq, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, req.URL, nil)
	if err != nil {
		http.Error(w, "invalid url", http.StatusBadRequest)
		return
	}
	pageReq.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Seminar/1.0; +https://seminar.app)")

	pageResp, err := http.DefaultClient.Do(pageReq)
	if err != nil {
		slog.Error("briefing page fetch failed", "url", req.URL, "err", err)
		http.Error(w, "could not fetch page", http.StatusBadGateway)
		return
	}
	defer pageResp.Body.Close()

	// Parse HTML and extract visible text, capping at 512 KB of source.
	doc, err := html.Parse(io.LimitReader(pageResp.Body, 512*1024))
	if err != nil {
		http.Error(w, "could not parse page", http.StatusInternalServerError)
		return
	}
	text := extractVisibleText(doc)
	// Truncate to ~4 000 chars so the prompt stays small and Haiku stays fast.
	if len(text) > 4000 {
		text = text[:4000]
	}
	if strings.TrimSpace(text) == "" {
		// Nothing to summarise (SPA, login wall, etc.) — return empty so the
		// frontend can silently skip showing the banner.
		writeJSON(w, http.StatusOK, map[string]string{"briefing": ""})
		return
	}

	// Ask Claude Haiku for a concise reading brief.
	system := "You are a pre-session reading assistant. Given a page's extracted text, write exactly 3 sentences for students about to read it: (1) what this text is and where it's from, (2) the central argument or topic, (3) one key concept or tension they should watch for. Be specific and direct. Do not open with \"This text\" or \"This article\"."
	userMsg := fmt.Sprintf("Page title: %s\n\nExtracted text:\n%s", req.Title, text)

	apiBody, _ := json.Marshal(map[string]any{
		"model":      "claude-haiku-4-5-20251001",
		"max_tokens": 220,
		"system":     system,
		"messages":   []map[string]string{{"role": "user", "content": userMsg}},
	})

	apiCtx, apiCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer apiCancel()

	apiReq, err := http.NewRequestWithContext(apiCtx, http.MethodPost,
		"https://api.anthropic.com/v1/messages", bytes.NewReader(apiBody))
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	apiReq.Header.Set("x-api-key", bh.apiKey)
	apiReq.Header.Set("anthropic-version", "2023-06-01")
	apiReq.Header.Set("content-type", "application/json")

	apiResp, err := http.DefaultClient.Do(apiReq)
	if err != nil {
		slog.Error("briefing claude call failed", "err", err)
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}
	defer apiResp.Body.Close()

	if apiResp.StatusCode != http.StatusOK {
		slog.Error("briefing claude non-200", "status", apiResp.StatusCode)
		http.Error(w, "upstream error", apiResp.StatusCode)
		return
	}

	var claudeResp struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.NewDecoder(apiResp.Body).Decode(&claudeResp); err != nil || len(claudeResp.Content) == 0 {
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"briefing": claudeResp.Content[0].Text})
}

// extractVisibleText walks the HTML node tree and collects text from visible
// content nodes, skipping script, style, nav, footer, header, and aside.
func extractVisibleText(n *html.Node) string {
	if n.Type == html.ElementNode {
		switch n.Data {
		case "script", "style", "noscript", "template",
			"nav", "footer", "header", "aside":
			return ""
		}
	}
	if n.Type == html.TextNode {
		t := strings.TrimSpace(n.Data)
		if t != "" {
			return t + " "
		}
		return ""
	}
	var sb strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		sb.WriteString(extractVisibleText(c))
	}
	return sb.String()
}
