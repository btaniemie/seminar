// ── WebSocket wire format ────────────────────────────────────────────────────

export type WsMessageType =
  | 'hello'
  | 'highlight'
  | 'cursor'
  | 'chat'
  | 'join'
  | 'leave'

export interface WsEnvelope<P = unknown> {
  type: WsMessageType
  sessionId: string
  clientId: string
  payload: P
}

export interface HighlightPayload {
  text: string
  url: string
}

// ── Background ↔ Content messages ────────────────────────────────────────────

export type BgRequest =
  | { type: 'GET_SESSION' }

export type BgResponse =
  | { sessionId: string }
  | { error: string }
