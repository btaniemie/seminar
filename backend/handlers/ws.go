package handlers

import (
	"crypto/rand"
	"fmt"
	"log"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/gorilla/websocket"
	"github.com/seminar/backend/hub"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	// Allow all origins for MVP — tighten to extension origin in production.
	CheckOrigin: func(r *http.Request) bool { return true },
}

// WSHandler handles WebSocket upgrade requests.
type WSHandler struct {
	hub *hub.Hub
}

func NewWSHandler(h *hub.Hub) *WSHandler {
	return &WSHandler{hub: h}
}

// ServeWS handles GET /ws?session=<id>
// Upgrades the connection and registers the client with the session room.
func (wh *WSHandler) ServeWS(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session")
	if sessionID == "" {
		http.Error(w, "missing session query param", http.StatusBadRequest)
		return
	}

	// Upgrade to WebSocket before touching session state.
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[ws] upgrade failed for session %s: %v", sessionID, err)
		return
	}

	session := wh.hub.GetOrCreate(sessionID)

	clientID, err := generateClientID()
	if err != nil {
		log.Printf("[ws] failed to generate client id: %v", err)
		conn.Close()
		return
	}

	// Optional ?name= param lets clients supply a display name for initials.
	name := r.URL.Query().Get("name")
	initials := initialsFor(name, clientID)
	color := session.NextColor()

	client := hub.NewClient(clientID, initials, color, session, conn)
	session.Join(client)

	// Send the client its own ID, initials, and color so it can render its own avatar.
	if err := conn.WriteJSON(map[string]any{
		"type":      "hello",
		"clientId":  clientID,
		"sessionId": sessionID,
		"initials":  initials,
		"color":     color,
	}); err != nil {
		log.Printf("[ws] failed to send hello to client %s: %v", clientID, err)
		conn.Close()
		return
	}

	// ReadPump and WritePump run concurrently; ReadPump owns the connection lifecycle.
	go client.WritePump()
	go client.ReadPump()
}

func generateClientID() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", b), nil
}

// initialsFor derives up to 2 uppercase initials from a display name.
// Falls back to the first 2 characters of the clientID if no name is given.
func initialsFor(name, clientID string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		// Use first 2 chars of clientID as a fallback.
		runes := []rune(clientID)
		if len(runes) >= 2 {
			return strings.ToUpper(string(runes[:2]))
		}
		return strings.ToUpper(clientID)
	}

	parts := strings.Fields(name)
	if len(parts) == 1 {
		runes := []rune(parts[0])
		end := 2
		if utf8.RuneCountInString(parts[0]) < 2 {
			end = utf8.RuneCountInString(parts[0])
		}
		return strings.ToUpper(string(runes[:end]))
	}

	// Multi-word name: first rune of each of the first two words.
	first := []rune(parts[0])
	second := []rune(parts[1])
	return strings.ToUpper(string(first[0]) + string(second[0]))
}
