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

// Range data for reconstructing a text selection on a peer's page
export interface RangeData {
  startXPath: string
  startOffset: number
  endXPath: string
  endOffset: number
}

export interface HighlightPayload extends RangeData {
  text: string
  url: string
}

export interface ChatPayload {
  role: 'user' | 'assistant'
  content: string
}

// ── Background ↔ Content messages ────────────────────────────────────────────

export type BgRequest =
  | { type: 'GET_SESSION' }
  | { type: 'JOIN_SESSION'; sessionId: string }

export type BgResponse =
  | { sessionId: string }
  | { error: string }
