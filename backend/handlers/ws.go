package handlers

import (
	"crypto/rand"
	"fmt"
	"log"
	"net/http"

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

	client := hub.NewClient(clientID, session, conn)
	session.Join(client)

	// Send the client its own ID so the extension can label its own events.
	type helloPayload struct {
		ClientID  string `json:"clientId"`
		SessionID string `json:"sessionId"`
	}
	if err := conn.WriteJSON(map[string]any{
		"type":      "hello",
		"clientId":  clientID,
		"sessionId": sessionID,
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
