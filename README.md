# Seminar

A collaborative reading Chrome extension with a real-time AI Socratic partner for college study groups.

## What it does

1. A user starts a session on any webpage → gets a shareable session link
2. Other users join via that link → everyone's text highlights and cursor positions sync in real time over WebSocket
3. A shared sidebar shows a conversation thread with an AI Socratic partner (Claude)
4. The AI asks questions — it never summarizes or completes work for you

## Architecture

```
seminar/
├── backend/          # Go WebSocket server + REST API
│   ├── main.go       # HTTP server, routes, CORS middleware
│   ├── hub/
│   │   ├── hub.go    # Session room manager
│   │   ├── session.go # Per-session broadcast loop
│   │   └── client.go  # Per-connection WebSocket client
│   └── handlers/
│       ├── session.go # POST/GET /api/session
│       └── ws.go      # GET /ws (WebSocket upgrade)
└── extension/        # Chrome extension (Manifest v3, React + TypeScript) [Phase 2+]
```

## Stack

- **Chrome Extension**: Manifest v3, React + TypeScript, Vite
- **Backend**: Go (`net/http`, `gorilla/websocket`)
- **AI**: Anthropic Claude API (`claude-sonnet-4-20250514`) with streaming
- **Session state**: In-memory Go maps (Redis later)
- **Auth**: None for MVP — sessions identified by short random ID

## API

### REST

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/session` | Create a new session → `{ sessionId }` |
| `GET` | `/api/session/:id` | Validate a session exists → `{ sessionId, status }` |

### WebSocket

Connect to `ws://localhost:8080/ws?session=<id>`

On connect the server sends a `hello` message:
```json
{ "type": "hello", "clientId": "a1b2c3d4", "sessionId": "e5f6a7b8" }
```

All subsequent messages use this envelope:
```json
{
  "type": "highlight" | "cursor" | "chat" | "join" | "leave",
  "sessionId": "e5f6a7b8",
  "clientId": "a1b2c3d4",
  "payload": { ... }
}
```

The `clientId` is stamped server-side — clients cannot spoof each other's identity.

## Running locally

### Prerequisites

- Go 1.22+
- (Phase 2) Node.js 20+

### Backend

```bash
cd backend
go mod tidy          # fetch gorilla/websocket
go run .             # starts on :8080

# Or with a custom port:
PORT=9000 go run .
```

### Quick smoke test

```bash
# Create a session
curl -X POST http://localhost:8080/api/session
# → {"sessionId":"a1b2c3d4"}

# Validate it
curl http://localhost:8080/api/session/a1b2c3d4
# → {"sessionId":"a1b2c3d4","status":"active"}

# Connect two WebSocket clients (requires wscat: npm i -g wscat)
wscat -c "ws://localhost:8080/ws?session=a1b2c3d4"
```

## Roadmap

- [x] Phase 1 — Go WebSocket server, session rooms, real-time broadcast
- [ ] Phase 2 — Chrome extension content script: text selection + sidebar injection
- [ ] Phase 3 — Real-time highlight sync between peers
- [ ] Phase 4 — Claude API integration with Socratic system prompt + streaming
- [ ] Phase 5 — Shareable session link flow
