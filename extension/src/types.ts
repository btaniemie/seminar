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

// ── Background ↔ Content messages ────────────────────────────────────────────

export type BgRequest =
  | { type: 'GET_SESSION' }

export type BgResponse =
  | { sessionId: string }
  | { error: string }
