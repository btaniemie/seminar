import React, { useEffect, useRef, useState } from 'react'
import type { BgRequest, BgResponse, HighlightPayload, WsEnvelope } from '../types'
import { applyHighlight, clearHighlight, serializeSelection } from './highlight'

const WS_BASE = 'ws://localhost:8080'

type Status = 'connecting' | 'connected' | 'disconnected'

interface HighlightEntry {
  clientId: string
  isSelf: boolean
  text: string
  timestamp: number
}

export function Sidebar() {
  const [collapsed, setCollapsed] = useState(false)
  const [sessionId, setSessionId] = useState<string | null>(null)
  const [clientId, setClientId] = useState<string | null>(null)
  const [status, setStatus] = useState<Status>('connecting')
  const [highlights, setHighlights] = useState<HighlightEntry[]>([])
  const wsRef = useRef<WebSocket | null>(null)
  // Ref so event listeners always read the current clientId without re-registering
  const clientIdRef = useRef<string | null>(null)

  // 1. Ask background SW for session ID, then open WebSocket
  useEffect(() => {
    const req: BgRequest = { type: 'GET_SESSION' }
    chrome.runtime.sendMessage(req, (res: BgResponse) => {
      if ('error' in res) {
        setStatus('disconnected')
        return
      }
      setSessionId(res.sessionId)
      connect(res.sessionId)
    })
    return () => wsRef.current?.close()
  }, [])

  function connect(sid: string) {
    const ws = new WebSocket(`${WS_BASE}/ws?session=${sid}`)
    wsRef.current = ws

    ws.onopen = () => setStatus('connected')
    ws.onclose = () => setStatus('disconnected')
    ws.onerror = () => setStatus('disconnected')

    ws.onmessage = (evt: MessageEvent<string>) => {
      try {
        const msg = JSON.parse(evt.data) as WsEnvelope

        if (msg.type === 'hello') {
          setClientId(msg.clientId)
          clientIdRef.current = msg.clientId

        } else if (msg.type === 'highlight') {
          const p = msg.payload as HighlightPayload
          const isSelf = msg.clientId === clientIdRef.current

          // Add to sidebar feed
          setHighlights(prev =>
            [
              { clientId: msg.clientId, isSelf, text: p.text, timestamp: Date.now() },
              ...prev,
            ].slice(0, 30)
          )

          // Apply visual highlight on the page for peers only.
          // Self already has the browser's native selection visible.
          if (!isSelf) {
            applyHighlight(msg.clientId, p)
          }

        } else if (msg.type === 'leave') {
          // Clear the departing peer's highlight from the page
          clearHighlight(msg.clientId)
          setHighlights(prev => prev.filter(h => h.clientId !== msg.clientId))
        }
      } catch {
        // malformed message — ignore
      }
    }
  }

  // 2. Capture text selections and broadcast them with serialized Range data
  useEffect(() => {
    function onMouseUp() {
      const sel = window.getSelection()
      const text = sel?.toString().trim()
      if (!text || text.length < 3) return

      const ws = wsRef.current
      if (!ws || ws.readyState !== WebSocket.OPEN) return

      const rangeData = serializeSelection(sel)
      if (!rangeData) return

      const payload: HighlightPayload = {
        text,
        url: window.location.href,
        ...rangeData,
      }
      ws.send(JSON.stringify({ type: 'highlight', payload }))
    }

    document.addEventListener('mouseup', onMouseUp)
    return () => document.removeEventListener('mouseup', onMouseUp)
  }, [])

  // ── Render ────────────────────────────────────────────────────────────────

  if (collapsed) {
    return (
      <div className="tab" onClick={() => setCollapsed(false)}>
        <span className="tab-label">Seminar</span>
        <span className={`dot dot--${status}`} />
      </div>
    )
  }

  return (
    <div className="sidebar">
      <header className="header">
        <span className="title">Seminar</span>
        <div className="header-right">
          <span className={`dot dot--${status}`} title={status} />
          <button className="btn-icon" onClick={() => setCollapsed(true)} title="Collapse">
            ›
          </button>
        </div>
      </header>

      <div className="session-info">
        <span className="label">Session</span>
        <code className="session-id">{sessionId ?? '…'}</code>
      </div>

      <div className="highlights">
        <p className="section-title">Highlights</p>
        {highlights.length === 0 ? (
          <p className="empty">Select text on the page to share a highlight.</p>
        ) : (
          highlights.map((h, i) => (
            <div key={i} className={`highlight-item ${h.isSelf ? 'highlight-item--self' : ''}`}>
              <span className="highlight-author">{h.isSelf ? 'You' : h.clientId.slice(0, 4)}</span>
              <p className="highlight-text">"{h.text}"</p>
            </div>
          ))
        )}
      </div>

      {/* Chat thread placeholder — wired up in Phase 4 */}
      <div className="chat-placeholder">
        <p className="empty">AI chat coming in Phase 4.</p>
      </div>
    </div>
  )
}
