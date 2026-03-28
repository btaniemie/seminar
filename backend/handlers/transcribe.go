package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
)

// TranscribeHandler handles POST /api/transcribe.
// It receives a multipart form upload (field "audio") and proxies it to the
// OpenAI Whisper API, returning { "text": "..." }.
type TranscribeHandler struct {
	apiKey string
}

func NewTranscribeHandler() *TranscribeHandler {
	return &TranscribeHandler{apiKey: os.Getenv("OPENAI_API_KEY")}
}

func (th *TranscribeHandler) Transcribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if th.apiKey == "" {
		http.Error(w, "OPENAI_API_KEY not configured", http.StatusInternalServerError)
		return
	}

	// 32 MB max — audio clips should be well under 1 MB
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("audio")
	if err != nil {
		http.Error(w, "audio field missing", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// ── Build multipart request for Whisper ──────────────────────────────────
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	// model field
	modelField, err := mw.CreateFormField("model")
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if _, err := modelField.Write([]byte("whisper-1")); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// audio file — preserve the content-type so Whisper knows the format
	contentType := header.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "audio/webm"
	}
	filename := header.Filename
	if filename == "" {
		filename = "audio.webm"
	}

	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition",
		fmt.Sprintf(`form-data; name="file"; filename="%s"`, filename))
	h.Set("Content-Type", contentType)
	fw, err := mw.CreatePart(h)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if _, err := io.Copy(fw, file); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	mw.Close()

	// ── Call Whisper ─────────────────────────────────────────────────────────
	apiReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
		"https://api.openai.com/v1/audio/transcriptions", &buf)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	apiReq.Header.Set("Authorization", "Bearer "+th.apiKey)
	apiReq.Header.Set("Content-Type", mw.FormDataContentType())

	apiResp, err := http.DefaultClient.Do(apiReq)
	if err != nil {
		log.Printf("[transcribe] whisper request failed: %v", err)
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}
	defer apiResp.Body.Close()

	if apiResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(apiResp.Body)
		log.Printf("[transcribe] whisper returned %d: %s", apiResp.StatusCode, body)
		http.Error(w, "upstream error", apiResp.StatusCode)
		return
	}

	var result struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(apiResp.Body).Decode(&result); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"text": result.Text}); err != nil {
		log.Printf("[transcribe] encode error: %v", err)
	}
}
