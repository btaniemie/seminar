# Seminar

A Chrome extension for collaborative reading with a Socratic AI. Built for college study groups.

**Demo:** _YouTube link coming soon_

## Why I built it

Three things converged:

1. I wanted to actually learn Go, not just do tutorials. I needed a project with real concurrency, WebSockets, and an HTTP server that does something meaningful.
2. I'm currently taking Computer Networks. Building Seminar made a lot of the course material click. WebSocket handshakes, TCP, chunked transfer encoding for SSE, the WebRTC signaling exchange - I implemented all of it, not just read about it.
3. I think most AI study tools are designed for one person. This one is built around the idea that thinking with other people, with an AI asking questions in the background, is a fundamentally different experience.

---

## What it does

Open Seminar on any webpage and invite your study group. Everyone's highlights sync in real time. Instead of a group chat, questions are anchored to specific passages as threads. The AI responds to those threads in the Socratic style -- it never gives you the answer, it asks a question back.

A few things happen automatically as the session runs:

- If two people highlight overlapping passages but seem to be reading them differently, Seminar detects the divergence and names the interpretive tension
- When you start the session, a short reading brief appears summarizing the page
- When you end the session, Seminar generates a comprehension map: what the group understood, where you got stuck, what went unresolved, and what to look at again before the exam

---

## Features

- Real-time highlight sync over WebSocket, with WebRTC P2P as a fallback
- Question threads anchored to highlighted passages
- Socratic AI responses (streaming)
- Presence avatars
- Session modes: Close Reading, Debate Prep, Exam Review
- Pre-session reading brief
- Divergence detection
- End-of-session comprehension map
- PDF support
- Shareable session links
- Markdown transcript export

---

## Stack

| Layer | Technology |
|-------|-----------|
| Extension | Manifest V3, React, TypeScript, Vite, Shadow DOM |
| Backend | Go (`net/http`, `gorilla/websocket`) |
| Real-time | WebSocket hub, WebRTC P2P data channels |
| AI | Anthropic Claude API (streaming SSE) |
| Persistence | Redis |
| Speech | Whisper via Anthropic transcription API |

---

## How to run it

**Prerequisites:** Go 1.22+, Node.js 20+, Redis, an [Anthropic API key](https://console.anthropic.com/)

### Backend

```bash
cd seminar/backend
cp .env.example .env   # add your ANTHROPIC_API_KEY
go mod tidy
go run .               # starts on :8080
```

### Extension

```bash
cd seminar/extension
npm install
npm run build          # outputs to dist/
```

In Chrome: go to `chrome://extensions`, enable Developer mode, click Load unpacked, select the `dist/` folder.

### Using it

1. Go to any article or webpage
2. Click the Seminar tab on the right edge of the screen
3. Copy the invite link and send it to your group
4. Start highlighting

---

## How I used AI

All technical decisions, system design, and feature ideas are mine. I designed the architecture, defined the data models, wrote the phase-by-phase feature specs, and made every call about what to build and how. Claude Code translated those decisions into working Go and TypeScript.

---

## Who made it

Minh Le -- submitted to the Dickinson Hackathon.
