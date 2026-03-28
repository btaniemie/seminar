package hub

import (
	"encoding/json"
	"log"
	"sync"
)

// Session is a room that broadcasts messages to all connected clients.
type Session struct {
	id        string
	mu        sync.RWMutex
	clients   map[*Client]bool
	broadcast chan []byte
	join      chan *Client
	leave     chan *Client
}

func newSession(id string) *Session {
	return &Session{
		id:        id,
		clients:   make(map[*Client]bool),
		broadcast: make(chan []byte, 256),
		join:      make(chan *Client),
		leave:     make(chan *Client),
	}
}

// run is the session's event loop — must be called in its own goroutine.
func (s *Session) run() {
	for {
		select {
		case c := <-s.join:
			s.mu.Lock()
			s.clients[c] = true
			s.mu.Unlock()
			log.Printf("[session %s] client %s joined (%d total)", s.id, c.ID, s.clientCount())

		case c := <-s.leave:
			s.mu.Lock()
			if _, ok := s.clients[c]; ok {
				delete(s.clients, c)
				close(c.send)
			}
			count := len(s.clients)
			s.mu.Unlock()
			log.Printf("[session %s] client %s left (%d remaining)", s.id, c.ID, count)

			// Broadcast presence update so peers know someone left.
			if count > 0 {
				s.broadcastPresence(c.ID, "leave")
			}

		case msg := <-s.broadcast:
			s.mu.RLock()
			for c := range s.clients {
				select {
				case c.send <- msg:
				default:
					// Slow client — drop and let it disconnect naturally.
					log.Printf("[session %s] dropping slow client %s", s.id, c.ID)
				}
			}
			s.mu.RUnlock()
		}
	}
}

func (s *Session) clientCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.clients)
}

// Broadcast sends a raw JSON message to every client in the session.
func (s *Session) Broadcast(msg []byte) {
	s.broadcast <- msg
}

// Join registers a client with this session.
func (s *Session) Join(c *Client) {
	s.join <- c
}

// Leave removes a client from this session.
func (s *Session) Leave(c *Client) {
	s.leave <- c
}

// broadcastPresence sends a synthetic join/leave event to all remaining clients.
func (s *Session) broadcastPresence(clientID, eventType string) {
	type presencePayload struct {
		ClientID string `json:"clientId"`
	}
	payload, _ := json.Marshal(presencePayload{ClientID: clientID})
	env := Envelope{
		Type:      eventType,
		SessionID: s.id,
		ClientID:  clientID,
		Payload:   json.RawMessage(payload),
	}
	data, err := json.Marshal(env)
	if err != nil {
		log.Printf("[session %s] failed to marshal presence event: %v", s.id, err)
		return
	}
	s.Broadcast(data)
}
