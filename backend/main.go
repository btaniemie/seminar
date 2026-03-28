package main

import (
	"log"
	"net/http"
	"os"

	"github.com/seminar/backend/handlers"
	"github.com/seminar/backend/hub"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	h := hub.NewHub()

	sessionHandler := handlers.NewSessionHandler(h)
	wsHandler := handlers.NewWSHandler(h)

	mux := http.NewServeMux()

	// REST endpoints
	mux.HandleFunc("POST /api/session", sessionHandler.CreateSession)
	mux.HandleFunc("GET /api/session/{id}", sessionHandler.GetSession)

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
