package main

import (
	"log"
	"net/http"
	"os"

	"github.com/joho/godotenv"
	"github.com/seminar/backend/handlers"
	"github.com/seminar/backend/hub"
	"github.com/seminar/backend/store"
)

func main() {
	// Load .env if present — silently ignored in production where env vars are set directly.
	if err := godotenv.Load(); err == nil {
		log.Println("loaded .env")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	st := store.New()
	h := hub.NewHub(st)
	hub.HydrateFromRedis(h, st) // restore persisted sessions from Redis

	sessionHandler := handlers.NewSessionHandler(h, st)
	wsHandler := handlers.NewWSHandler(h)
	chatHandler := handlers.NewChatHandler(h)
	transcribeHandler := handlers.NewTranscribeHandler()
	threadHandler := handlers.NewThreadHandler(h, st)
	briefingHandler := handlers.NewBriefingHandler()
	divergenceChecker := handlers.NewDivergenceChecker(st, h)
	h.SetDivergenceFunc(divergenceChecker.Check)
	comprehensionHandler := handlers.NewComprehensionHandler(st, h)

	mux := http.NewServeMux()

	// REST endpoints
	mux.HandleFunc("POST /api/session", sessionHandler.CreateSession)
	mux.HandleFunc("GET /api/session/{id}", sessionHandler.GetSession)
	mux.HandleFunc("POST /api/chat", chatHandler.Chat)
	mux.HandleFunc("POST /api/transcribe", transcribeHandler.Transcribe)
	mux.HandleFunc("POST /api/briefing", briefingHandler.Briefing)
	mux.HandleFunc("POST /api/comprehension", comprehensionHandler.Comprehension)

	// Thread endpoints (Phase 11)
	mux.HandleFunc("POST /api/threads", threadHandler.Create)
	mux.HandleFunc("POST /api/threads/{id}/reply", threadHandler.AddReply)
	mux.HandleFunc("POST /api/threads/{id}/ask", threadHandler.AskAI)
	mux.HandleFunc("GET /api/threads/{sessionId}", threadHandler.GetBySession)

	// WebSocket endpoint
	mux.HandleFunc("GET /ws", wsHandler.ServeWS)

	// Wrap the mux with CORS middleware so the Chrome extension can reach the API.
	handler := corsMiddleware(mux)

	log.Printf("seminar backend listening on :%s", port)
	if err := http.ListenAndServe(":"+port, handler); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

// corsMiddleware adds permissive CORS headers for MVP.
// Tighten AllowedOrigins to the extension's chrome-extension:// origin in production.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
