package hub

import (
	"encoding/json"
	"log"
	"sync"
)

// PresenceUser is the per-user data broadcast in presence events.
type PresenceUser struct {
	ClientID string `json:"clientId"`
	Initials string `json:"initials"`
	Color    string `json:"color"`
}

// HighlightEntry records a single text selection for the session's rolling buffer.
type HighlightEntry struct {
	ClientID string `json:"clientId"`
	Initials string `json:"initials"`
	Text     string `json:"text"`
}

var presenceColors = []string{
	"#FACC15", // yellow
	"#4ADE80", // green
	"#60A5FA", // blue
	"#FB7185", // red
	"#C084FC", // purple
	"#2DD4BF", // teal
}

const maxHighlightBuf = 10

// DirectMsg is a message targeted at a single named client (used for WebRTC signaling).
type DirectMsg struct {
	To   string
	Data []byte
}

// Session is a room that broadcasts messages to all connected clients.
type Session struct {
	id           string
	mu           sync.RWMutex
	clients      map[*Client]bool
	clientsByID  map[string]*Client // guarded by run() goroutine only
	broadcast    chan []byte
	direct       chan DirectMsg     // for targeted WebRTC signaling messages
	join         chan *Client
	leave        chan *Client
	nextColorIdx int              // guarded by mu; incremented on each client join
	highlightBuf []HighlightEntry // rolling buffer of last 10 highlights; guarded by mu
	mode         string           // "close-reading" | "debate-prep" | "exam-review"; guarded by mu
	hostID       string           // clientId of first joiner; guarded by mu
}

func newSession(id string) *Session {
	return &Session{
		id:          id,
		clients:     make(map[*Client]bool),
		clientsByID: make(map[string]*Client),
		broadcast:   make(chan []byte, 256),
		direct:      make(chan DirectMsg, 256),
		join:        make(chan *Client),
		leave:       make(chan *Client),
	}
}

// SendToClient routes a message directly to a single client by its ID.
// Safe to call from any goroutine — goes through the direct channel which
// is consumed exclusively by the run() goroutine.
func (s *Session) SendToClient(to string, data []byte) {
	s.direct <- DirectMsg{To: to, Data: data}
}

// NextColor assigns the next color from the palette to a joining client.
// Thread-safe — called from the WS handler goroutine, not from run().
func (s *Session) NextColor() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	color := presenceColors[s.nextColorIdx%len(presenceColors)]
	s.nextColorIdx++
	return color
}

// AddHighlight appends a highlight to the session's rolling buffer (capped at maxHighlightBuf).
// Thread-safe — called from client ReadPump goroutines.
func (s *Session) AddHighlight(clientID, initials, text string) {
	s.mu.Lock()
	s.highlightBuf = append(s.highlightBuf, HighlightEntry{
		ClientID: clientID,
		Initials: initials,
		Text:     text,
	})
	if len(s.highlightBuf) > maxHighlightBuf {
		s.highlightBuf = s.highlightBuf[len(s.highlightBuf)-maxHighlightBuf:]
	}
	s.mu.Unlock()
	go s.persist()
}

// GetHighlights returns a copy of the current highlight buffer.
// Thread-safe — called from the HTTP handler goroutine.
func (s *Session) GetHighlights() []HighlightEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]HighlightEntry, len(s.highlightBuf))
	copy(result, s.highlightBuf)
	return result
}

// SetMode updates the session mode if the caller is the host, then fans the new mode out.
// Safe to call from any goroutine.
func (s *Session) SetMode(callerID, mode string) {
	s.mu.Lock()
	if s.hostID != callerID {
		s.mu.Unlock()
		return
	}
	s.mode = mode
	s.mu.Unlock()
	s.broadcastModeUpdate()
	go s.persist()
}

// GetMode returns the current session mode.
func (s *Session) GetMode() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.mode
}

// HostID returns the clientId of the session host.
func (s *Session) HostID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.hostID
}

// broadcastModeUpdate fans out { type:"mode", payload:{mode,hostId} } via the broadcast channel.
// Safe to call from any goroutine.
func (s *Session) broadcastModeUpdate() {
	s.mu.RLock()
	mode := s.mode
	hostID := s.hostID
	s.mu.RUnlock()

	type modePayload struct {
		Mode   string `json:"mode"`
		HostID string `json:"hostId"`
	}
	payload, _ := json.Marshal(modePayload{Mode: mode, HostID: hostID})
	env := Envelope{Type: "mode", SessionID: s.id, Payload: json.RawMessage(payload)}
	data, _ := json.Marshal(env)
	s.Broadcast(data) // goes through broadcast channel — safe from any goroutine
}

// modeFanout sends { type:"mode" } directly to all clients.
// Must only be called from the run() goroutine.
func (s *Session) modeFanout() {
	s.mu.RLock()
	mode := s.mode
	hostID := s.hostID
	s.mu.RUnlock()

	type modePayload struct {
		Mode   string `json:"mode"`
		HostID string `json:"hostId"`
	}
	payload, _ := json.Marshal(modePayload{Mode: mode, HostID: hostID})
	env := Envelope{Type: "mode", SessionID: s.id, Payload: json.RawMessage(payload)}
	data, _ := json.Marshal(env)
	s.sendToAll(data)
}

// run is the session's event loop — must be called in its own goroutine.
func (s *Session) run() {
	for {
		select {
		case c := <-s.join:
			s.mu.Lock()
			s.clients[c] = true
			if s.hostID == "" {
				s.hostID = c.ID
			}
			s.mu.Unlock()
			s.clientsByID[c.ID] = c // only written from run()
			log.Printf("[session %s] client %s joined (%d total)", s.id, c.ID, s.clientCount())
			// Broadcast updated participant list and current mode to everyone.
			s.presenceFanout()
			s.modeFanout()
			go s.persist()

		case c := <-s.leave:
			s.mu.Lock()
			if _, ok := s.clients[c]; ok {
				delete(s.clients, c)
				close(c.send)
			}
			count := len(s.clients)
			s.mu.Unlock()
			delete(s.clientsByID, c.ID) // only written from run()
			log.Printf("[session %s] client %s left (%d remaining)", s.id, c.ID, count)

			if count > 0 {
				// Send a leave event first so peers can clear this client's highlights.
				s.sendLeaveEvent(c.ID)
				// Then broadcast the updated (smaller) participant list.
				s.presenceFanout()
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

		case dm := <-s.direct:
			// Deliver a WebRTC signaling message to one specific client.
			if c, ok := s.clientsByID[dm.To]; ok {
				select {
				case c.send <- dm.Data:
				default:
					log.Printf("[session %s] dropping direct msg for %s", s.id, dm.To)
				}
			}
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

// sendLeaveEvent sends { type: "leave", clientId } to all remaining clients.
// Must only be called from the run() goroutine.
func (s *Session) sendLeaveEvent(clientID string) {
	env := Envelope{
		Type:      "leave",
		SessionID: s.id,
		ClientID:  clientID,
		Payload:   json.RawMessage(`{}`),
	}
	data, err := json.Marshal(env)
	if err != nil {
		log.Printf("[session %s] failed to marshal leave event: %v", s.id, err)
		return
	}
	s.sendToAll(data)
}

// presenceFanout sends { type: "presence", payload: [PresenceUser...] } to all clients.
// Must only be called from the run() goroutine (accesses s.clients without the lock).
func (s *Session) presenceFanout() {
	users := make([]PresenceUser, 0, len(s.clients))
	for c := range s.clients {
		users = append(users, PresenceUser{
			ClientID: c.ID,
			Initials: c.Initials,
			Color:    c.Color,
		})
	}
	payload, err := json.Marshal(users)
	if err != nil {
		log.Printf("[session %s] failed to marshal presence payload: %v", s.id, err)
		return
	}
	env := Envelope{
		Type:      "presence",
		SessionID: s.id,
		Payload:   json.RawMessage(payload),
	}
	data, err := json.Marshal(env)
	if err != nil {
		log.Printf("[session %s] failed to marshal presence envelope: %v", s.id, err)
		return
	}
	s.sendToAll(data)
}

// sendToAll directly fans a message out to every client's send channel.
// Must only be called from the run() goroutine.
func (s *Session) sendToAll(data []byte) {
	for c := range s.clients {
		select {
		case c.send <- data:
		default:
			log.Printf("[session %s] dropping message for slow client %s", s.id, c.ID)
		}
	}
}
