import React, { useEffect, useRef, useState } from 'react'
import { z } from 'zod'
import type {
  BgRequest, BgResponse,
  ChatPayload, HighlightPayload, ModePayload, PresenceUser, SessionMode, WsEnvelope,
  RtcOfferPayload, RtcAnswerPayload, RtcIcePayload,
  ThreadWithReplies,
} from '../types'
import { applyHighlight, clearHighlight, serializeSelection } from './highlight'

const WS_BASE  = 'ws://localhost:8080'
const API_BASE = 'http://localhost:8080'

// ── Zod schemas for thread API response validation ───────────────────────────

const ReplySchema = z.object({
  id:        z.string(),
  threadId:  z.string(),
  authorId:  z.string(),
  content:   z.string(),
  isAI:      z.boolean(),
  createdAt: z.string(),
})

const ThreadSchema = z.object({
  id:          z.string(),
  sessionId:   z.string(),
  anchorText:  z.string(),
  anchorRange: z.string(),
  authorId:    z.string(),
  question:    z.string(),
  createdAt:   z.string(),
  replies:     z.array(ReplySchema),
})

const ThreadsArraySchema = z.array(ThreadSchema)

// ── Local types ──────────────────────────────────────────────────────────────

type Status = 'connecting' | 'connected' | 'disconnected'

interface HighlightEntry {
  clientId:  string
  isSelf:    boolean
  text:      string
  initials:  string
  color:     string
  timestamp: number
}

interface ChatMsg {
  id:        string
  role:      'user' | 'assistant'
  content:   string
  clientId?: string
  timestamp: number
}

const MODE_LABELS: Record<SessionMode, string> = {
  'close-reading': 'Close Reading',
  'debate-prep':   'Debate Prep',
  'exam-review':   'Exam Review',
}
const MODES = Object.keys(MODE_LABELS) as SessionMode[]

// ── Component ────────────────────────────────────────────────────────────────

export function Sidebar() {
  const [collapsed, setCollapsed]     = useState(false)
  const [sessionId, setSessionId]     = useState<string | null>(null)
  const [clientId, setClientId]       = useState<string | null>(null)
  const [myInitials, setMyInitials]   = useState('')
  const [myColor, setMyColor]         = useState('')
  const [participants, setParticipants] = useState<PresenceUser[]>([])
  const [mode, setMode]               = useState<SessionMode | ''>('')
  const [hostId, setHostId]           = useState<string | null>(null)
  const [status, setStatus]           = useState<Status>('connecting')
  const [highlights, setHighlights]   = useState<HighlightEntry[]>([])
  const [chatMessages, setChatMessages] = useState<ChatMsg[]>([])
  const [streamingText, setStreamingText] = useState('')
  const [inputText, setInputText]     = useState('')
  const [isStreaming, setIsStreaming] = useState(false)
  const [copied, setCopied]           = useState(false)
  const [isRecording, setIsRecording] = useState(false)
  const [micError, setMicError]       = useState<string | null>(null)
  const [isPdf, setIsPdf]             = useState(false)
  const [pdfRedirecting, setPdfRedirecting] = useState(false)

  // Phase 11 state
  const [threads, setThreads]                   = useState<ThreadWithReplies[]>([])
  const [activeTab, setActiveTab]               = useState<'threads' | 'chat' | 'summary'>('chat')
  const [pendingHighlight, setPendingHighlight] = useState<{ text: string; range: string } | null>(null)
  const [replyTexts, setReplyTexts]             = useState<Record<string, string>>({})
  const [askingAI, setAskingAI]                 = useState<Record<string, boolean>>({})
  const [streamingReplies, setStreamingReplies] = useState<Record<string, string>>({})

  // Phase 12 state
  const [briefing, setBriefing]               = useState<string | null>(null)
  const [briefingDismissed, setBriefingDismissed] = useState(false)

  // Phase 13a state
  interface DivergenceNotice { passage: string; message: string }
  const [divergences, setDivergences] = useState<DivergenceNotice[]>([])

  // Phase 13b state
  interface ComprehensionMap {
    understood: string[]
    friction: string[]
    unresolved: string[]
    recommended_followup: string[]
  }
  const [comprehensionMap, setComprehensionMap] = useState<ComprehensionMap | null>(null)
  const [isEndingSession, setIsEndingSession]   = useState(false)
  const [endSessionError, setEndSessionError]   = useState<string | null>(null)

  const wsRef              = useRef<WebSocket | null>(null)
  const mediaRecorderRef   = useRef<MediaRecorder | null>(null)
  const audioChunksRef     = useRef<Blob[]>([])
  const peersRef           = useRef<Map<string, RTCPeerConnection>>(new Map())
  const dataChannelsRef    = useRef<Map<string, RTCDataChannel>>(new Map())
  const clientIdRef        = useRef<string | null>(null)
  const messagesEndRef     = useRef<HTMLDivElement>(null)
  const highlightsRef      = useRef<HighlightEntry[]>([])
  const participantMapRef  = useRef<Map<string, PresenceUser>>(new Map())
  const myInitialsRef      = useRef('')
  const myColorRef         = useRef('')
  // Ref for pendingHighlight so the mouseup handler always reads the latest value
  const pendingHighlightRef = useRef<{ text: string; range: string } | null>(null)

  // Auto-scroll chat to bottom on new messages / streaming
  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [chatMessages, streamingText])

  // Detect native PDF pages
  useEffect(() => {
    const isPdfPage =
      document.contentType === 'application/pdf' ||
      (window.location.pathname.toLowerCase().endsWith('.pdf') &&
        !!document.querySelector('embed[type="application/pdf"]'))
    setIsPdf(isPdfPage)
  }, [])

  // Get session from background SW, then open WebSocket
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
          // Load existing threads for this session
          fetchThreads(sid)
          // Fetch a reading brief for this page
          fetchBriefing()

        } else if (msg.type === 'thread_new') {
          const t = msg.payload as ThreadWithReplies
          setThreads(prev => prev.some(x => x.id === t.id) ? prev : [...prev, t])
          setActiveTab('threads')

        } else if (msg.type === 'thread_update') {
          const t = msg.payload as ThreadWithReplies
          setThreads(prev => prev.map(x => x.id === t.id ? t : x))
          // Clear streaming state now that the saved reply has arrived
          setStreamingReplies(prev => { const n = { ...prev }; delete n[t.id]; return n })
          setAskingAI(prev => { const n = { ...prev }; delete n[t.id]; return n })

        } else if (msg.type === 'session_ended') {
          const p = msg.payload as { comprehension: ComprehensionMap }
          if (p.comprehension) {
            setComprehensionMap(p.comprehension)
            setActiveTab('summary')
          }

        } else if (msg.type === 'divergence') {
          const p = msg.payload as { passage: string; message: string }
          setDivergences(prev => [...prev, p])

        } else if (msg.type === 'rtc_offer') {
          const p = msg.payload as RtcOfferPayload
          handleRtcOffer(msg.clientId, p.sdp).catch(console.error)

        } else if (msg.type === 'rtc_answer') {
          const p = msg.payload as RtcAnswerPayload
          const pc = peersRef.current.get(msg.clientId)
          if (pc) pc.setRemoteDescription({ type: 'answer', sdp: p.sdp }).catch(console.error)

        } else if (msg.type === 'rtc_ice') {
          const p = msg.payload as RtcIcePayload
          const pc = peersRef.current.get(msg.clientId)
          if (pc) pc.addIceCandidate(p.candidate).catch(console.error)

        } else if (msg.type === 'presence') {
          const users = msg.payload as PresenceUser[]
          setParticipants(users)
          const map = new Map<string, PresenceUser>()
          users.forEach(u => map.set(u.clientId, u))
          participantMapRef.current = map

          const myId = clientIdRef.current
          if (myId) {
            for (const u of users) {
              if (u.clientId !== myId && !peersRef.current.has(u.clientId) && myId > u.clientId) {
                initiateWebRTC(u.clientId).catch(console.error)
              }
            }
          }

        } else if (msg.type === 'mode') {
          const p = msg.payload as ModePayload
          setMode(p.mode)
          setHostId(p.hostId)

        } else if (msg.type === 'highlight') {
          if (!isSelf && dataChannelsRef.current.has(msg.clientId)) {
            const dc = dataChannelsRef.current.get(msg.clientId)!
            if (dc.readyState === 'open') return
          }

          const p = msg.payload as HighlightPayload
          const pUser = isSelf
            ? { clientId: msg.clientId, initials: myInitialsRef.current || msg.clientId.slice(0, 2).toUpperCase(), color: myColorRef.current || '#A8A29E' }
            : (participantMapRef.current.get(msg.clientId) ?? { clientId: msg.clientId, initials: msg.clientId.slice(0, 2).toUpperCase(), color: '#A8A29E' })
          const entry: HighlightEntry = { clientId: msg.clientId, isSelf, text: p.text, initials: pUser.initials, color: pUser.color, timestamp: Date.now() }
          setHighlights(prev => {
            const next = [entry, ...prev].slice(0, 10)
            highlightsRef.current = next
            return next
          })
          if (!isSelf) applyHighlight(msg.clientId, p, pUser.initials, pUser.color, pdfScrollEl())

        } else if (msg.type === 'chat') {
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
          peersRef.current.get(msg.clientId)?.close()
          peersRef.current.delete(msg.clientId)
          dataChannelsRef.current.delete(msg.clientId)
        }
      } catch { /* malformed message */ }
    }
  }

  // Capture text selections → broadcast highlight + set as pending anchor for thread creation
  useEffect(() => {
    function onMouseUp() {
      const sel = window.getSelection()
      const text = sel?.toString().trim()
      if (!text || text.length < 3) return
      const ws = wsRef.current
      if (!ws || ws.readyState !== WebSocket.OPEN) return

      const rangeData = serializeSelection(sel!)
      if (!rangeData) return

      // Set pending highlight so the next chat submit creates a thread
      const ph = { text, range: JSON.stringify(rangeData) }
      setPendingHighlight(ph)
      pendingHighlightRef.current = ph

      const payload: HighlightPayload = {
        text, url: window.location.href, ...rangeData,
      }
      ws.send(JSON.stringify({ type: 'highlight', payload }))

      const myId = clientIdRef.current
      if (myId) {
        applyHighlight(myId, payload, myInitialsRef.current || '?', myColorRef.current || '#A8A29E', pdfScrollEl())
      }

      for (const dc of dataChannelsRef.current.values()) {
        if (dc.readyState === 'open') {
          try { dc.send(JSON.stringify(payload)) } catch { /* ignore */ }
        }
      }
    }
    document.addEventListener('mouseup', onMouseUp)
    return () => document.removeEventListener('mouseup', onMouseUp)
  }, [])

  // ── Thread functions ─────────────────────────────────────────────────────────

  async function fetchThreads(sid: string) {
    try {
      const resp = await fetch(`${API_BASE}/api/threads/${sid}`)
      if (!resp.ok) return
      const raw: unknown = await resp.json()
      const parsed = ThreadsArraySchema.safeParse(raw)
      if (parsed.success) setThreads(parsed.data)
    } catch { /* silently skip — non-critical */ }
  }

  async function fetchBriefing() {
    try {
      const resp = await fetch(`${API_BASE}/api/briefing`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ url: window.location.href, title: document.title }),
      })
      if (!resp.ok) return
      const { briefing: text } = await resp.json() as { briefing: string }
      if (text) setBriefing(text)
    } catch { /* non-critical — silently skip */ }
  }

  async function endSession() {
    if (!sessionId || isEndingSession) return
    setIsEndingSession(true)
    setEndSessionError(null)
    try {
      const resp = await fetch(`${API_BASE}/api/comprehension`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          sessionId,
          pageTitle: document.title,
          url: window.location.href,
          mode,
          highlights: highlights.map(h => ({ clientId: h.clientId, initials: h.initials, text: h.text })),
          threads: threads.map(t => ({
            anchorText: t.anchorText,
            question: t.question,
            authorId: t.authorId,
            replies: t.replies.map(r => ({ authorId: r.authorId, content: r.content, isAI: r.isAI })),
          })),
          chatHistory: chatMessages.map(m => ({ role: m.role, content: m.content })),
        }),
      })
      if (!resp.ok || !resp.body) throw new Error(`HTTP ${resp.status}`)

      const reader  = resp.body.getReader()
      const decoder = new TextDecoder()
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
            const parsed = JSON.parse(line.slice(6)) as {
              done?: boolean
              comprehension?: ComprehensionMap
              error?: string
            }
            if (parsed.done && parsed.comprehension) {
              setComprehensionMap(parsed.comprehension)
              setActiveTab('summary')
            }
          } catch { /* ignore partial chunks */ }
        }
      }
    } catch (err) {
      console.error('[seminar] endSession error:', err)
      setEndSessionError(err instanceof Error ? err.message : 'Failed to generate summary')
    } finally {
      setIsEndingSession(false)
    }
  }

  async function createThread(anchorText: string, anchorRange: string, question: string) {
    if (!sessionId || !clientId) return
    try {
      const resp = await fetch(`${API_BASE}/api/threads`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ sessionId, anchorText, anchorRange, authorId: clientId, question }),
      })
      if (!resp.ok) return
      const raw: unknown = await resp.json()
      const parsed = ThreadSchema.safeParse(raw)
      if (parsed.success) {
        // Optimistic add; WS thread_new will deduplicate
        setThreads(prev => prev.some(x => x.id === parsed.data.id) ? prev : [...prev, parsed.data])
      }
    } catch (err) {
      console.error('[seminar] createThread error:', err)
    }
  }

  async function addHumanReply(threadId: string) {
    const content = replyTexts[threadId]?.trim()
    if (!content || !clientId) return
    setReplyTexts(prev => ({ ...prev, [threadId]: '' }))
    try {
      const resp = await fetch(`${API_BASE}/api/threads/${threadId}/reply`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ authorId: clientId, content }),
      })
      if (!resp.ok) return
      const raw: unknown = await resp.json()
      const parsed = ThreadSchema.safeParse(raw)
      if (parsed.success) {
        // Update immediately; WS thread_update will also arrive and is harmless
        setThreads(prev => prev.map(t => t.id === threadId ? parsed.data : t))
      }
    } catch (err) {
      console.error('[seminar] addHumanReply error:', err)
    }
  }

  async function askSeminar(threadId: string) {
    setAskingAI(prev => ({ ...prev, [threadId]: true }))
    setStreamingReplies(prev => ({ ...prev, [threadId]: '' }))
    try {
      const resp = await fetch(`${API_BASE}/api/threads/${threadId}/ask`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ pageTitle: document.title, url: window.location.href }),
      })
      if (!resp.ok || !resp.body) throw new Error(`HTTP ${resp.status}`)

      const reader  = resp.body.getReader()
      const decoder = new TextDecoder()
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
              setStreamingReplies(prev => ({ ...prev, [threadId]: (prev[threadId] ?? '') + parsed.text }))
            }
          } catch { /* ignore */ }
        }
      }
      // Don't clear askingAI / streamingReplies here — the WS thread_update will do it
      // once the server persists and broadcasts the completed reply.
    } catch (err) {
      console.error('[seminar] askSeminar error:', err)
      setAskingAI(prev => { const n = { ...prev }; delete n[threadId]; return n })
      setStreamingReplies(prev => { const n = { ...prev }; delete n[threadId]; return n })
    }
  }

  // ── Chat / message sending ───────────────────────────────────────────────────

  async function sendMessage() {
    const text = inputText.trim()
    if (!text || !sessionId) return

    // If there's a pending highlight, create a thread anchored to it
    const ph = pendingHighlight
    if (ph) {
      setPendingHighlight(null)
      pendingHighlightRef.current = null
      setInputText('')
      await createThread(ph.text, ph.range, text)
      setActiveTab('threads')
      return
    }

    if (isStreaming) return

    const userMsg: ChatMsg = {
      id: crypto.randomUUID(), role: 'user', content: text,
      clientId: clientId ?? undefined, timestamp: Date.now(),
    }

    setChatMessages(prev => [...prev, userMsg])
    setInputText('')
    setIsStreaming(true)
    setStreamingText('')

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
          messages: [...chatMessages, userMsg].map(m => ({ role: m.role, content: m.content })),
          context: {
            highlight: highlightsRef.current[0]?.text ?? '',
            pageTitle: document.title,
            pageUrl: window.location.href,
          },
        }),
      })

      if (!resp.ok || !resp.body) throw new Error(`HTTP ${resp.status}`)

      const reader  = resp.body.getReader()
      const decoder = new TextDecoder()
      let fullText = ''
      let buffer   = ''

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

  function pdfScrollEl(): HTMLElement | undefined {
    return document.getElementById('seminar-pdf-scroll') ?? undefined
  }

  // ── WebRTC helpers ───────────────────────────────────────────────────────────

  const RTC_CONFIG: RTCConfiguration = {
    iceServers: [{ urls: 'stun:stun.l.google.com:19302' }],
  }

  function setupDataChannel(dc: RTCDataChannel, peerId: string) {
    dc.onopen  = () => { dataChannelsRef.current.set(peerId, dc) }
    dc.onclose = () => { dataChannelsRef.current.delete(peerId) }
    dc.onerror = () => { dataChannelsRef.current.delete(peerId) }
    dc.onmessage = (evt: MessageEvent<string>) => {
      try {
        const payload = JSON.parse(evt.data) as HighlightPayload
        const pUser = participantMapRef.current.get(peerId) ??
          { clientId: peerId, initials: peerId.slice(0, 2).toUpperCase(), color: '#A8A29E' }
        const entry: HighlightEntry = {
          clientId: peerId, isSelf: false,
          text: payload.text, initials: pUser.initials, color: pUser.color,
          timestamp: Date.now(),
        }
        setHighlights(prev => {
          const next = [entry, ...prev].slice(0, 10)
          highlightsRef.current = next
          return next
        })
        applyHighlight(peerId, payload, pUser.initials, pUser.color, pdfScrollEl())
      } catch { /* malformed DC message */ }
    }
  }

  async function initiateWebRTC(peerId: string) {
    const pc = new RTCPeerConnection(RTC_CONFIG)
    peersRef.current.set(peerId, pc)
    const dc = pc.createDataChannel('highlights', { ordered: false, maxRetransmits: 0 })
    setupDataChannel(dc, peerId)
    pc.onicecandidate = (e) => {
      if (!e.candidate) return
      wsRef.current?.send(JSON.stringify({
        type: 'rtc_ice', to: peerId,
        payload: { candidate: e.candidate.toJSON() } satisfies RtcIcePayload,
      }))
    }
    pc.onconnectionstatechange = () => {
      if (pc.connectionState === 'failed' || pc.connectionState === 'closed') {
        peersRef.current.delete(peerId)
        dataChannelsRef.current.delete(peerId)
      }
    }
    const offer = await pc.createOffer()
    await pc.setLocalDescription(offer)
    wsRef.current?.send(JSON.stringify({
      type: 'rtc_offer', to: peerId,
      payload: { sdp: offer.sdp } satisfies RtcOfferPayload,
    }))
  }

  async function handleRtcOffer(fromId: string, sdp: string) {
    const pc = new RTCPeerConnection(RTC_CONFIG)
    peersRef.current.set(fromId, pc)
    pc.ondatachannel = (e) => setupDataChannel(e.channel, fromId)
    pc.onicecandidate = (e) => {
      if (!e.candidate) return
      wsRef.current?.send(JSON.stringify({
        type: 'rtc_ice', to: fromId,
        payload: { candidate: e.candidate.toJSON() } satisfies RtcIcePayload,
      }))
    }
    pc.onconnectionstatechange = () => {
      if (pc.connectionState === 'failed' || pc.connectionState === 'closed') {
        peersRef.current.delete(fromId)
        dataChannelsRef.current.delete(fromId)
      }
    }
    await pc.setRemoteDescription({ type: 'offer', sdp })
    const answer = await pc.createAnswer()
    await pc.setLocalDescription(answer)
    wsRef.current?.send(JSON.stringify({
      type: 'rtc_answer', to: fromId,
      payload: { sdp: answer.sdp } satisfies RtcAnswerPayload,
    }))
  }

  // ── Misc helpers ─────────────────────────────────────────────────────────────

  function openPdfViewer() {
    setPdfRedirecting(true)
    const req: BgRequest = { type: 'OPEN_PDF_VIEWER', pdfUrl: window.location.href }
    chrome.runtime.sendMessage(req, () => { /* tab navigates away */ })
  }

  async function toggleRecording() {
    if (isRecording && mediaRecorderRef.current) {
      mediaRecorderRef.current.stop()
      return
    }

    setMicError(null)
    let stream: MediaStream
    try {
      stream = await navigator.mediaDevices.getUserMedia({ audio: true })
    } catch {
      setMicError('Microphone access denied')
      return
    }

    const mimeType = MediaRecorder.isTypeSupported('audio/webm;codecs=opus')
      ? 'audio/webm;codecs=opus'
      : 'audio/webm'

    const recorder = new MediaRecorder(stream, { mimeType })
    mediaRecorderRef.current = recorder
    audioChunksRef.current   = []

    recorder.ondataavailable = (e) => {
      if (e.data.size > 0) audioChunksRef.current.push(e.data)
    }

    recorder.onstop = async () => {
      stream.getTracks().forEach(t => t.stop())
      setIsRecording(false)
      const blob = new Blob(audioChunksRef.current, { type: mimeType })
      audioChunksRef.current = []
      const form = new FormData()
      form.append('audio', blob, 'audio.webm')
      try {
        const resp = await fetch(`${API_BASE}/api/transcribe`, { method: 'POST', body: form })
        if (!resp.ok) throw new Error(`HTTP ${resp.status}`)
        const { text } = await resp.json() as { text: string }
        if (text) setInputText(prev => (prev ? prev + ' ' + text : text))
      } catch (err) {
        console.error('[seminar] transcribe error:', err)
        setMicError('Transcription failed')
      }
    }

    recorder.start()
    setIsRecording(true)
    setTimeout(() => {
      if (mediaRecorderRef.current?.state === 'recording') mediaRecorderRef.current.stop()
    }, 60_000)
  }

  function sendModeChange(newMode: SessionMode) {
    if (!wsRef.current || wsRef.current.readyState !== WebSocket.OPEN) return
    wsRef.current.send(JSON.stringify({ type: 'set_mode', payload: { mode: newMode } }))
  }

  function exportTranscript() {
    const pageTitle  = document.title
    const pageUrl    = window.location.href
    const date       = new Date().toLocaleDateString()
    const sessionLabel = sessionId ?? 'session'
    const modeLabel  = mode ? MODE_LABELS[mode] : 'General'

    let md = `# Seminar Session — ${pageTitle}\n\n`
    md += `**Page:** ${pageUrl}\n`
    md += `**Date:** ${date}\n`
    md += `**Mode:** ${modeLabel}\n`
    md += `**Session:** ${sessionLabel}\n\n`

    if (comprehensionMap) {
      md += `## Session Summary\n\n`
      md += `### Understood\n\n`
      comprehensionMap.understood.forEach(s => { md += `- ${s}\n` })
      md += `\n### Friction\n\n`
      comprehensionMap.friction.forEach(s => { md += `- ${s}\n` })
      md += `\n### Unresolved\n\n`
      comprehensionMap.unresolved.forEach(s => { md += `- ${s}\n` })
      md += `\n### Follow-up\n\n`
      comprehensionMap.recommended_followup.forEach(s => { md += `- ${s}\n` })
      md += '\n'
    }

    if (threads.length > 0) {
      md += `## Threads\n\n`
      for (const t of threads) {
        md += `### "${t.anchorText.slice(0, 80)}${t.anchorText.length > 80 ? '…' : ''}"\n\n`
        md += `**Q:** ${t.question}\n\n`
        for (const r of t.replies) {
          const who = r.isAI ? 'Seminar' : r.authorId === clientId ? 'You' : 'Peer'
          md += `**${who}:** ${r.content}\n\n`
        }
        md += '---\n\n'
      }
    }

    if (highlights.length > 0) {
      md += `## Highlights\n\n`
      for (const h of [...highlights].reverse()) {
        const t   = new Date(h.timestamp).toLocaleTimeString()
        const who = h.isSelf ? 'You' : h.initials
        md += `- **${who}** (${t}): "${h.text}"\n`
      }
      md += '\n'
    }

    if (chatMessages.length > 0) {
      md += `## Discussion\n\n`
      for (const m of chatMessages) {
        const t   = new Date(m.timestamp).toLocaleTimeString()
        const who = m.role === 'assistant'
          ? 'Seminar'
          : m.clientId === clientId
            ? 'You'
            : (participantMapRef.current.get(m.clientId ?? '')?.initials ?? 'Peer')
        md += `**${who}** _(${t})_\n\n${m.content}\n\n---\n\n`
      }
    }

    const blob   = new Blob([md], { type: 'text/markdown' })
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

  function dismissPendingHighlight() {
    setPendingHighlight(null)
    pendingHighlightRef.current = null
  }

  // ── Render ───────────────────────────────────────────────────────────────────

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

      {/* Session bar */}
      <div className="session-bar">
        <code className="session-id">{sessionId ?? '…'}</code>
        <div className="session-bar-actions">
          <button className="invite-btn" onClick={copyLink} disabled={!sessionId}>
            {copied ? 'Copied ✓' : 'Invite'}
          </button>
          <button
            className="export-btn"
            onClick={exportTranscript}
            disabled={chatMessages.length === 0 && highlights.length === 0 && threads.length === 0}
            title="Export session as Markdown"
          >
            Export
          </button>
          <button
            className="end-session-btn"
            onClick={endSession}
            disabled={isEndingSession || !!comprehensionMap || !sessionId}
            title="End session and generate comprehension map"
          >
            {isEndingSession ? 'Analyzing…' : 'End session'}
          </button>
        </div>
      </div>
      {endSessionError && (
        <div className="end-session-error">{endSessionError}</div>
      )}

      {/* Mode selector */}
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

      {/* PDF banner */}
      {isPdf && (
        <div className="pdf-banner">
          <span className="pdf-banner-text">PDF detected</span>
          <button className="pdf-open-btn" onClick={openPdfViewer} disabled={pdfRedirecting}>
            {pdfRedirecting ? 'Opening…' : 'Open in Seminar viewer →'}
          </button>
        </div>
      )}

      {/* Briefing banner */}
      {briefing && !briefingDismissed && (
        <div className="briefing-banner">
          <div className="briefing-banner-body">
            <span className="briefing-label">Reading Brief</span>
            <p className="briefing-text">{briefing}</p>
          </div>
          <button className="briefing-dismiss" onClick={() => setBriefingDismissed(true)} title="Dismiss">✕</button>
        </div>
      )}

      {/* Highlights strip — top 3 */}
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

      {/* Tab switcher */}
      <div className="tab-bar">
        <button
          className={`tab-bar-btn${activeTab === 'threads' ? ' tab-bar-btn--active' : ''}`}
          onClick={() => setActiveTab('threads')}
        >
          Threads
          {threads.length > 0 && (
            <span className="tab-badge">{threads.length}</span>
          )}
        </button>
        <button
          className={`tab-bar-btn${activeTab === 'chat' ? ' tab-bar-btn--active' : ''}`}
          onClick={() => setActiveTab('chat')}
        >
          Chat
        </button>
        {comprehensionMap && (
          <button
            className={`tab-bar-btn${activeTab === 'summary' ? ' tab-bar-btn--active' : ''}`}
            onClick={() => setActiveTab('summary')}
          >
            Summary
          </button>
        )}
      </div>

      {/* Threads panel */}
      {activeTab === 'threads' && (
        <div className="threads-panel">
          {/* Divergence callout cards */}
          {divergences.map((d, i) => (
            <div key={i} className="divergence-card">
              <span className="divergence-label">Seminar noticed something —</span>
              <blockquote className="divergence-passage">{d.passage}</blockquote>
              <p className="divergence-message">{d.message}</p>
            </div>
          ))}

          {threads.length === 0 ? (
            <p className="threads-empty">
              Select a passage, then type a question to start a thread.
            </p>
          ) : (
            threads.map(t => (
              <div key={t.id} className="thread-card">
                {/* Anchor passage */}
                <blockquote className="thread-anchor">{t.anchorText}</blockquote>

                {/* Question */}
                <p className="thread-question">{t.question}</p>

                {/* Replies */}
                {(t.replies.length > 0 || streamingReplies[t.id] !== undefined) && (
                  <div className="thread-replies">
                    {t.replies.map(r => (
                      <div key={r.id} className={`thread-reply${r.isAI ? ' thread-reply--ai' : ''}`}>
                        <span className="msg-label">
                          {r.isAI ? 'Seminar' : r.authorId === clientId ? 'You' : 'Peer'}
                        </span>
                        <p className="msg-content">{r.content}</p>
                      </div>
                    ))}
                    {/* Streaming AI reply (before WS thread_update arrives) */}
                    {streamingReplies[t.id] !== undefined && streamingReplies[t.id] !== '' && (
                      <div className="thread-reply thread-reply--ai">
                        <span className="msg-label">Seminar</span>
                        <p className="msg-content">
                          {streamingReplies[t.id]}
                          <span className="cursor" />
                        </p>
                      </div>
                    )}
                  </div>
                )}

                {/* Reply area */}
                <div className="thread-reply-area">
                  <input
                    className="thread-reply-input"
                    placeholder="Add a reply…"
                    value={replyTexts[t.id] ?? ''}
                    onChange={e => setReplyTexts(prev => ({ ...prev, [t.id]: e.target.value }))}
                    onKeyDown={e => {
                      if (e.key === 'Enter' && !e.shiftKey) {
                        e.preventDefault()
                        addHumanReply(t.id)
                      }
                    }}
                  />
                  <div className="thread-actions">
                    <button
                      className="thread-reply-btn"
                      onClick={() => addHumanReply(t.id)}
                      disabled={!replyTexts[t.id]?.trim()}
                    >
                      Reply
                    </button>
                    <button
                      className="thread-ask-btn"
                      onClick={() => askSeminar(t.id)}
                      disabled={!!askingAI[t.id]}
                    >
                      {askingAI[t.id] ? '…' : 'Ask Seminar'}
                    </button>
                  </div>
                </div>
              </div>
            ))
          )}
        </div>
      )}

      {/* Chat panel */}
      {activeTab === 'chat' && (
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
      )}

      {/* Summary panel */}
      {activeTab === 'summary' && comprehensionMap && (
        <div className="summary-panel">
          <div className="summary-section">
            <h3 className="summary-section-title summary-section-title--understood">Understood</h3>
            <ul className="summary-list">
              {comprehensionMap.understood.map((item, i) => (
                <li key={i} className="summary-item">{item}</li>
              ))}
            </ul>
          </div>
          <div className="summary-section">
            <h3 className="summary-section-title summary-section-title--friction">Friction</h3>
            <ul className="summary-list">
              {comprehensionMap.friction.map((item, i) => (
                <li key={i} className="summary-item">{item}</li>
              ))}
            </ul>
          </div>
          <div className="summary-section">
            <h3 className="summary-section-title summary-section-title--unresolved">Unresolved</h3>
            <ul className="summary-list">
              {comprehensionMap.unresolved.map((item, i) => (
                <li key={i} className="summary-item">{item}</li>
              ))}
            </ul>
          </div>
          <div className="summary-section">
            <h3 className="summary-section-title summary-section-title--followup">Follow-up</h3>
            <ul className="summary-list">
              {comprehensionMap.recommended_followup.map((item, i) => (
                <li key={i} className="summary-item">{item}</li>
              ))}
            </ul>
          </div>
        </div>
      )}

      {/* Input area */}
      <div className="chat-input-area">
        {/* Anchor indicator — shown when a highlight is pending */}
        {pendingHighlight && (
          <div className="anchor-indicator">
            <span className="anchor-indicator-text">
              Thread: "{pendingHighlight.text.length > 55
                ? pendingHighlight.text.slice(0, 55) + '…'
                : pendingHighlight.text}"
            </span>
            <button className="anchor-dismiss" onClick={dismissPendingHighlight} title="Cancel thread">✕</button>
          </div>
        )}
        <textarea
          className="chat-input"
          placeholder={pendingHighlight
            ? 'Ask a question about this passage…'
            : 'What are you thinking about…'}
          value={inputText}
          onChange={e => setInputText(e.target.value)}
          onKeyDown={onKeyDown}
          disabled={(!pendingHighlight && isStreaming) || status !== 'connected'}
          rows={2}
        />
        <div className="input-footer">
          <span className="input-hint">
            {pendingHighlight ? 'Enter to create thread' : 'Enter to send · Shift+Enter for newline'}
          </span>
          <button
            className="ask-btn"
            onClick={sendMessage}
            disabled={(!pendingHighlight && isStreaming) || !inputText.trim() || status !== 'connected'}
          >
            {pendingHighlight ? 'Thread →' : isStreaming ? '…' : 'Ask →'}
          </button>
        </div>
      </div>
    </div>
  )
}
