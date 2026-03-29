# Seminar

A collaborative reading Chrome extension with a real-time AI Socratic partner for college study groups.

## Demo

_YouTube link coming soon_

---

## What it does

Seminar turns any webpage or article into a shared reading room. Students in the same session can:

- **Highlight passages** together — highlights sync in real time across all open tabs
- **Thread questions** anchored to specific passages — instead of a flat chat, every question lives next to the text that prompted it
- **Get Socratic AI responses** — the AI never just gives you the answer; it asks a question that pushes your thinking further
- **Detect divergence** — if two students highlight the same passage but seem to be reading it differently, Seminar surfaces the interpretive tension automatically
- **End the session** with a comprehension map — a structured summary of what the group understood, where they struggled, what went unresolved, and what to revisit before the exam

I built it because studying alone with an AI tutor is fine, but studying _with people_ while an AI listens and asks the right questions at the right moment is something different. The goal was to make a tool that makes group study sessions actually productive.

---

## Why I built it

A few reasons came together at once:

1. I wanted a real project to learn **Go** — not just tutorials, but something with concurrency, WebSockets, and a real HTTP server under pressure.
2. I'm taking **Computer Networks** right now, and building this has grounded a lot of the course material. WebSocket handshakes, TCP connections, HTTP/1.1 chunked transfer encoding for SSE streaming, the WebRTC signaling dance — I've implemented all of it here, not just read about it.
3. I genuinely think the product is useful. Most AI study tools are solo. This one is built for groups.

---

## Who made it

**Minh Le**

---

## Stack

| Layer | Technology |
|-------|-----------|
| Chrome Extension | Manifest V3, React, TypeScript, Vite, Shadow DOM |
| Backend | Go (`net/http`, `gorilla/websocket`) |
| Real-time | WebSocket fan-out hub, WebRTC P2P data channels |
| AI | Anthropic Claude API — streaming SSE, Socratic prompts |
| Persistence | Redis (sessions, highlights, chat, threads, comprehension maps) |
| Speech | Whisper via Anthropic transcription API |

---

## Features

- Real-time highlight sync (WebSocket + WebRTC P2P fallback)
- Presence avatars with per-user colors
- Session modes: Close Reading, Debate Prep, Exam Review
- Question threads anchored to highlighted passages
- Socratic AI responses (streaming)
- Pre-session reading brief (Claude Haiku summarizes the page before you start)
- Divergence detection (Claude flags when two students read the same passage differently)
- End-of-session comprehension map (Understood / Friction / Unresolved / Follow-up)
- Voice input via microphone transcription
- PDF viewer with highlight support
- Shareable session links
- Markdown transcript export

---

## How to run it

### Prerequisites

- Go 1.22+
- Node.js 20+
- Redis running locally (`redis-server`)
- An [Anthropic API key](https://console.anthropic.com/)

### 1. Backend

```bash
cd seminar/backend
cp .env.example .env        # then add your ANTHROPIC_API_KEY
go mod tidy
go run .                    # starts on :8080
```

### 2. Extension

```bash
cd seminar/extension
npm install
npm run build               # outputs to dist/
```

Then in Chrome:
1. Go to `chrome://extensions`
2. Enable **Developer mode**
3. Click **Load unpacked** and select the `dist/` folder

### 3. Use it

1. Navigate to any article or webpage
2. Click the **Seminar** tab on the right edge of the screen
3. Share the session link with others (click **Invite**)
4. Start highlighting and asking questions

---

## How I used AI

I used **Claude Code** as a coding assistant throughout this project. The workflow was:

- I designed the system architecture (WebSocket hub, Redis key schema, extension/backend split)
- I wrote detailed phase-by-phase feature specifications describing exactly what to build
- Claude Code generated the implementation code from those specs
- I reviewed, tested, and debugged everything — and made all decisions about what to change when something didn't work

All technical decisions, system design, and feature ideas are mine. I used **Claude Code** to generate implementation code from my specifications — I designed the architecture, defined the data models, wrote the phase-by-phase feature specs, and made every call about what to build and how. Claude Code translated those decisions into working Go and TypeScript.