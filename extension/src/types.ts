// ── WebSocket wire format ────────────────────────────────────────────────────

export type WsMessageType =
  | 'hello'
  | 'highlight'
  | 'cursor'
  | 'chat'
  | 'join'
  | 'leave'
  | 'presence'
  | 'mode'
  | 'set_mode'
  | 'rtc_offer'
  | 'rtc_answer'
  | 'rtc_ice'

export type SessionMode = 'close-reading' | 'debate-prep' | 'exam-review'

export interface ModePayload {
  mode: SessionMode | ''
  hostId: string
}

export interface WsEnvelope<P = unknown> {
  type: WsMessageType
  sessionId: string
  clientId: string
  to?: string       // target clientId for directed WebRTC signaling messages
  // Present only on 'hello' messages (sent flat, not in payload)
  initials?: string
  color?: string
  payload: P
}

// ── WebRTC signaling payloads ────────────────────────────────────────────────

export interface RtcOfferPayload  { sdp: string }
export interface RtcAnswerPayload { sdp: string }
export interface RtcIcePayload    { candidate: RTCIceCandidateInit }

// One entry in a presence broadcast
export interface PresenceUser {
  clientId: string
  initials: string
  color: string
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
  | { type: 'OPEN_PDF_VIEWER'; pdfUrl: string }

export type BgResponse =
  | { sessionId: string }
  | { ok: true }
  | { error: string }
