package hub

import (
	"encoding/json"
	"log"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10 // slightly less than pongWait
	maxMessageSize = 32 * 1024            // 32 KB
)

// Envelope is the wire format for all WebSocket messages.
type Envelope struct {
	Type      string          `json:"type"`      // "highlight" | "cursor" | "chat" | "join" | "leave"
	SessionID string          `json:"sessionId"`
	ClientID  string          `json:"clientId"`
	Payload   json.RawMessage `json:"payload"`
}

// Client represents one connected browser tab.
type Client struct {
	ID       string
	Initials string // e.g. "AB" — derived from display name or clientID
	Color    string // hex color assigned on join, e.g. "#60A5FA"
	session  *Session
	conn     *websocket.Conn
	send     chan []byte
}

func NewClient(id, initials, color string, session *Session, conn *websocket.Conn) *Client {
	return &Client{
		ID:       id,
		Initials: initials,
		Color:    color,
		session:  session,
		conn:     conn,
		send:     make(chan []byte, 64),
	}
}

// ReadPump pumps messages from the WebSocket to the session broadcast channel.
// Must be called in its own goroutine. Closes itself and signals Leave on exit.
func (c *Client) ReadPump() {
	defer func() {
		c.session.Leave(c)
		c.conn.Close()
	}()

	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, raw, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseGoingAway,
				websocket.CloseAbnormalClosure,
			) {
				log.Printf("[client %s] read error: %v", c.ID, err)
			}
			break
		}

		// Stamp the clientId server-side so clients can't spoof each other.
		var env Envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			log.Printf("[client %s] bad envelope: %v", c.ID, err)
			continue
		}
		env.ClientID = c.ID
		env.SessionID = c.session.id

		stamped, err := json.Marshal(env)
		if err != nil {
			log.Printf("[client %s] marshal error: %v", c.ID, err)
			continue
		}

		c.session.Broadcast(stamped)
	}
}

// WritePump pumps messages from the send channel to the WebSocket.
// Must be called in its own goroutine.
func (c *Client) WritePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case msg, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// Channel was closed by session.Leave.
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				log.Printf("[client %s] write error: %v", c.ID, err)
				return
			}

		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
