import React, { useEffect, useRef, useState } from 'react'
import type {
  BgRequest, BgResponse,
  ChatPayload, HighlightPayload, ModePayload, PresenceUser, SessionMode, WsEnvelope,
} from '../types'
import { applyHighlight, clearHighlight, serializeSelection } from './highlight'

const WS_BASE = 'ws://localhost:8080'
const API_BASE = 'http://localhost:8080'

type Status = 'connecting' | 'connected' | 'disconnected'

interface HighlightEntry {
  clientId: string
  isSelf: boolean
  text: string
  initials: string
  color: string
  timestamp: number
}

interface ChatMsg {
  id: string
  role: 'user' | 'assistant'
  content: string
  clientId?: string
  timestamp: number
}

const MODE_LABELS: Record<SessionMode, string> = {
  'close-reading': 'Close Reading',
  'debate-prep':   'Debate Prep',
  'exam-review':   'Exam Review',
}
const MODES = Object.keys(MODE_LABELS) as SessionMode[]

export function Sidebar() {
  const [collapsed, setCollapsed] = useState(false)
  const [sessionId, setSessionId] = useState<string | null>(null)
  const [clientId, setClientId] = useState<string | null>(null)
  const [myInitials, setMyInitials] = useState('')
  const [myColor, setMyColor] = useState('')
  const [participants, setParticipants] = useState<PresenceUser[]>([])
  const [mode, setMode] = useState<SessionMode | ''>('')
  const [hostId, setHostId] = useState<string | null>(null)
  const [status, setStatus] = useState<Status>('connecting')
  const [highlights, setHighlights] = useState<HighlightEntry[]>([])
  const [chatMessages, setChatMessages] = useState<ChatMsg[]>([])
  const [streamingText, setStreamingText] = useState('')
  const [inputText, setInputText] = useState('')
  const [isStreaming, setIsStreaming] = useState(false)
  const [copied, setCopied] = useState(false)

  const wsRef = useRef<WebSocket | null>(null)
  const clientIdRef = useRef<string | null>(null)
  const messagesEndRef = useRef<HTMLDivElement>(null)
  // Keep a ref to highlights so sendMessage can read the latest without a stale closure
  const highlightsRef = useRef<HighlightEntry[]>([])
  // Always-current lookup of clientId → presence info (avoids stale closure issues)
  const participantMapRef = useRef<Map<string, PresenceUser>>(new Map())
  // Own initials/color stored as refs so highlight handlers can read them without stale closures
  const myInitialsRef = useRef('')
  const myColorRef = useRef('')

  // Auto-scroll chat to bottom on new messages / streaming
  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [chatMessages, streamingText])

  // Get session from background SW, then open WebSocket.
  // If the page URL contains ?seminar_session=<id>, join that session
  // instead of creating a new one (this is the shareable link entry point).
  useEffect(() => {
    const urlParam = new URLSearchParams(window.location.search).get('seminar_session')
    const req: BgRequest = urlParam
      ? { type: 'JOIN_SESSION', sessionId: urlParam }
      : { type: 'GET_SESSION' }

    chrome.runtime.sendMessage(req, (res: BgResponse) => {
      if ('error' in res) { setStatus('disconnected'); return }
      setSessionId(res.sessionId)
      connect(res.sessionId)
    })
    return () => wsRef.current?.close()
  }, [])

  function connect(sid: string) {
    const ws = new WebSocket(`${WS_BASE}/ws?session=${sid}`)
    wsRef.current = ws
    ws.onopen  = () => setStatus('connected')
    ws.onclose = () => setStatus('disconnected')
    ws.onerror = () => setStatus('disconnected')

    ws.onmessage = (evt: MessageEvent<string>) => {
      try {
        const msg = JSON.parse(evt.data) as WsEnvelope
        const isSelf = msg.clientId === clientIdRef.current

        if (msg.type === 'hello') {
          setClientId(msg.clientId)
          clientIdRef.current = msg.clientId
          if (msg.initials) {
            setMyInitials(msg.initials)
            myInitialsRef.current = msg.initials
          }
          if (msg.color) {
            setMyColor(msg.color)
            myColorRef.current = msg.color
          }

        } else if (msg.type === 'presence') {
          const users = msg.payload as PresenceUser[]
          setParticipants(users)
          // Keep the ref in sync for use in non-React callbacks
          const map = new Map<string, PresenceUser>()
          users.forEach(u => map.set(u.clientId, u))
          participantMapRef.current = map

        } else if (msg.type === 'mode') {
          const p = msg.payload as ModePayload
          setMode(p.mode)
          setHostId(p.hostId)

        } else if (msg.type === 'highlight') {
          const p = msg.payload as HighlightPayload
          // Look up presence info so the feed and overlay use consistent colors/names
          const pUser = isSelf
            ? { clientId: msg.clientId, initials: myInitialsRef.current || msg.clientId.slice(0, 2).toUpperCase(), color: myColorRef.current || '#A8A29E' }
            : (participantMapRef.current.get(msg.clientId) ?? { clientId: msg.clientId, initials: msg.clientId.slice(0, 2).toUpperCase(), color: '#A8A29E' })
          const entry: HighlightEntry = { clientId: msg.clientId, isSelf, text: p.text, initials: pUser.initials, color: pUser.color, timestamp: Date.now() }
          setHighlights(prev => {
            const next = [entry, ...prev].slice(0, 10)
            highlightsRef.current = next
            return next
          })
          if (!isSelf) applyHighlight(msg.clientId, p, pUser.initials, pUser.color)

        } else if (msg.type === 'chat') {
          // Only add peer messages — our own are already in local state
          if (!isSelf) {
            const p = msg.payload as ChatPayload
            setChatMessages(prev => [...prev, {
              id: crypto.randomUUID(),
              role: p.role,
              content: p.content,
              clientId: msg.clientId,
              timestamp: Date.now(),
            }])
          }

        } else if (msg.type === 'leave') {
          clearHighlight(msg.clientId)
          setHighlights(prev => prev.filter(h => h.clientId !== msg.clientId))
        }
      } catch { /* malformed message */ }
    }
  }

  // Capture text selections and broadcast them
  useEffect(() => {
    function onMouseUp() {
      const sel = window.getSelection()
      const text = sel?.toString().trim()
      if (!text || text.length < 3) return
      const ws = wsRef.current
      if (!ws || ws.readyState !== WebSocket.OPEN) return

      const rangeData = serializeSelection(sel!)
      if (!rangeData) return

      const payload: HighlightPayload = {
        text, url: window.location.href, ...rangeData,
      }
      ws.send(JSON.stringify({ type: 'highlight', payload }))
    }
    document.addEventListener('mouseup', onMouseUp)
    return () => document.removeEventListener('mouseup', onMouseUp)
  }, [])

  async function sendMessage() {
    const text = inputText.trim()
    if (!text || isStreaming || !sessionId) return

    const userMsg: ChatMsg = {
      id: crypto.randomUUID(), role: 'user', content: text,
      clientId: clientId ?? undefined, timestamp: Date.now(),
    }

    setChatMessages(prev => [...prev, userMsg])
    setInputText('')
    setIsStreaming(true)
    setStreamingText('')

    // Broadcast user message so peers see it immediately
    wsRef.current?.send(JSON.stringify({
      type: 'chat',
      payload: { role: 'user', content: text } as ChatPayload,
    }))

    try {
      const resp = await fetch(`${API_BASE}/api/chat`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          sessionId,
          // Send the full conversation history (up to this user message)
          messages: [...chatMessages, userMsg].map(m => ({ role: m.role, content: m.content })),
          context: {
            highlight: highlightsRef.current[0]?.text ?? '',
            pageTitle: document.title,
            pageUrl: window.location.href,
          },
        }),
      })

      if (!resp.ok || !resp.body) throw new Error(`HTTP ${resp.status}`)

      // Parse SSE stream
      const reader = resp.body.getReader()
      const decoder = new TextDecoder()
      let fullText = ''
      let buffer = ''

      while (true) {
        const { done, value } = await reader.read()
        if (done) break

        buffer += decoder.decode(value, { stream: true })
        const lines = buffer.split('\n')
        buffer = lines.pop() ?? ''

        for (const line of lines) {
          if (!line.startsWith('data: ')) continue
          try {
            const parsed = JSON.parse(line.slice(6)) as { text?: string; done?: boolean }
            if (parsed.text) {
              fullText += parsed.text
              setStreamingText(fullText)
            }
          } catch { /* ignore */ }
        }
      }

      const aiMsg: ChatMsg = { id: crypto.randomUUID(), role: 'assistant', content: fullText, timestamp: Date.now() }
      setChatMessages(prev => [...prev, aiMsg])
      setStreamingText('')

      // Broadcast completed AI response to peers
      wsRef.current?.send(JSON.stringify({
        type: 'chat',
        payload: { role: 'assistant', content: fullText } as ChatPayload,
      }))

    } catch (err) {
      console.error('[seminar] chat error:', err)
      setChatMessages(prev => [...prev, {
        id: crypto.randomUUID(), role: 'assistant',
        content: 'Could not reach the backend. Is the server running?',
        timestamp: Date.now(),
      }])
    } finally {
      setIsStreaming(false)
      setStreamingText('')
    }
  }

  function sendModeChange(newMode: SessionMode) {
    if (!wsRef.current || wsRef.current.readyState !== WebSocket.OPEN) return
    wsRef.current.send(JSON.stringify({
      type: 'set_mode',
      payload: { mode: newMode },
    }))
  }

  function exportTranscript() {
    const pageTitle = document.title
    const pageUrl = window.location.href
    const date = new Date().toLocaleDateString()
    const sessionLabel = sessionId ?? 'session'
    const modeLabel = mode ? MODE_LABELS[mode] : 'General'

    let md = `# Seminar Session — ${pageTitle}\n\n`
    md += `**Page:** ${pageUrl}\n`
    md += `**Date:** ${date}\n`
    md += `**Mode:** ${modeLabel}\n`
    md += `**Session:** ${sessionLabel}\n\n`

    if (highlights.length > 0) {
      md += `## Highlights\n\n`
      for (const h of [...highlights].reverse()) {
        const t = new Date(h.timestamp).toLocaleTimeString()
        const who = h.isSelf ? 'You' : h.initials
        md += `- **${who}** (${t}): "${h.text}"\n`
      }
      md += '\n'
    }

    if (chatMessages.length > 0) {
      md += `## Discussion\n\n`
      for (const m of chatMessages) {
        const t = new Date(m.timestamp).toLocaleTimeString()
        const who = m.role === 'assistant'
          ? 'Seminar'
          : m.clientId === clientId
            ? 'You'
            : (participantMapRef.current.get(m.clientId ?? '')?.initials ?? 'Peer')
        md += `**${who}** _(${t})_\n\n${m.content}\n\n---\n\n`
      }
    }

    const blob = new Blob([md], { type: 'text/markdown' })
    const blobUrl = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = blobUrl
    a.download = `seminar-${sessionLabel}.md`
    a.click()
    URL.revokeObjectURL(blobUrl)
  }

  function copyLink() {
    if (!sessionId) return
    const url = new URL(window.location.href)
    url.searchParams.set('seminar_session', sessionId)
    navigator.clipboard.writeText(url.toString())
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }

  function onKeyDown(e: React.KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      sendMessage()
    }
  }

  // ── Render ──────────────────────────────────────────────────────────────────

  if (collapsed) {
    return (
      <div className="tab" onClick={() => setCollapsed(false)}>
        <span className="tab-label">Seminar</span>
        <span className={`status-pip status-pip--${status}`} />
      </div>
    )
  }

  return (
    <div className="sidebar">
      {/* Header */}
      <header className="header">
        <span className="wordmark">Seminar</span>
        <div className="header-right">
          {/* Presence avatars — one dot per connected participant */}
          {participants.length > 0 && (
            <div className="avatar-row">
              {participants.map(p => (
                <div
                  key={p.clientId}
                  className={`avatar${p.clientId === clientId ? ' avatar--self' : ''}`}
                  style={{ backgroundColor: p.color }}
                  title={p.clientId === clientId ? `You (${p.initials})` : p.initials}
                >
                  {p.initials}
                </div>
              ))}
            </div>
          )}
          <span className={`status-pip status-pip--${status}`} title={status} />
          <button className="collapse-btn" onClick={() => setCollapsed(true)} title="Collapse">‹</button>
        </div>
      </header>

      {/* Session */}
      <div className="session-bar">
        <code className="session-id">{sessionId ?? '…'}</code>
        <div className="session-bar-actions">
          <button
            className="invite-btn"
            onClick={copyLink}
            disabled={!sessionId}
          >
            {copied ? 'Copied ✓' : 'Invite'}
          </button>
          <button
            className="export-btn"
            onClick={exportTranscript}
            disabled={chatMessages.length === 0 && highlights.length === 0}
            title="Export session as Markdown"
          >
            Export
          </button>
        </div>
      </div>

      {/* Mode selector — visible to all, editable only by host */}
      <div className="mode-bar">
        {MODES.map(m => (
          <button
            key={m}
            className={`mode-btn${mode === m ? ' mode-btn--active' : ''}`}
            onClick={() => sendModeChange(m)}
            disabled={clientId !== hostId}
            title={clientId !== hostId ? 'Only the session host can change the mode' : undefined}
          >
            {MODE_LABELS[m]}
          </button>
        ))}
      </div>

      {/* Highlights */}
      {highlights.length > 0 && (
        <div className="highlights-strip">
          {highlights.slice(0, 3).map((h, i) => (
            <div key={i} className="hl-item">
              <span className="hl-who">
                <span className="hl-dot" style={{ backgroundColor: h.color }} />
                {h.isSelf ? 'You' : h.initials}
              </span>
              <span className="hl-quote">
                {h.text.length > 80 ? h.text.slice(0, 80) + '…' : h.text}
              </span>
            </div>
          ))}
        </div>
      )}

      {/* Chat */}
      <div className="chat-messages">
        {chatMessages.length === 0 && !streamingText && (
          <p className="chat-empty">
            Select a passage, then ask a question.{'\n'}I won't give you the answer — I'll help you find it.
          </p>
        )}
        {chatMessages.map(msg => (
          <div key={msg.id} className={`message message--${msg.role}`}>
            <span className="msg-label" style={
              msg.role === 'user' && msg.clientId && msg.clientId !== clientId
                ? { color: participantMapRef.current.get(msg.clientId)?.color }
                : undefined
            }>
              {msg.role === 'assistant'
                ? 'Seminar'
                : msg.clientId === clientId
                  ? 'You'
                  : (participantMapRef.current.get(msg.clientId ?? '')?.initials ?? 'Peer')}
            </span>
            <p className="msg-content">{msg.content}</p>
          </div>
        ))}
        {streamingText && (
          <div className="message message--assistant">
            <span className="msg-label">Seminar</span>
            <p className="msg-content">{streamingText}<span className="cursor" /></p>
          </div>
        )}
        <div ref={messagesEndRef} />
      </div>

      {/* Input */}
      <div className="chat-input-area">
        <textarea
          className="chat-input"
          placeholder="What are you thinking about…"
          value={inputText}
          onChange={e => setInputText(e.target.value)}
          onKeyDown={onKeyDown}
          disabled={isStreaming || status !== 'connected'}
          rows={2}
        />
        <div className="input-footer">
          <span className="input-hint">Enter to send · Shift+Enter for newline</span>
          <button
            className="ask-btn"
            onClick={sendMessage}
            disabled={isStreaming || !inputText.trim() || status !== 'connected'}
          >
            {isStreaming ? '…' : 'Ask →'}
          </button>
        </div>
      </div>
    </div>
  )
}
