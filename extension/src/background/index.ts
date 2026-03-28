import type { BgRequest, BgResponse } from '../types'

const API_BASE = 'http://localhost:8080'

chrome.runtime.onMessage.addListener(
  (msg: BgRequest, _sender, sendResponse: (r: BgResponse) => void) => {
    if (msg.type === 'GET_SESSION') {
      getOrCreateSession().then(sendResponse).catch(() =>
        sendResponse({ error: 'failed to get session' })
      )
      return true
    }

    if (msg.type === 'JOIN_SESSION') {
      joinSession(msg.sessionId).then(sendResponse).catch(() =>
        sendResponse({ error: 'failed to join session' })
      )
      return true
    }

    if (msg.type === 'OPEN_PDF_VIEWER') {
      const viewerUrl =
        chrome.runtime.getURL('pdf-viewer.html') +
        '?file=' + encodeURIComponent(msg.pdfUrl)
      chrome.tabs.update({ url: viewerUrl })
        .then(() => sendResponse({ ok: true as const }))
        .catch(() => sendResponse({ error: 'could not update tab' }))
      return true
    }
  }
)

async function getOrCreateSession(): Promise<BgResponse> {
  const stored = await chrome.storage.session.get('sessionId')
  if (stored.sessionId) {
    return { sessionId: stored.sessionId as string }
  }

  const res = await fetch(`${API_BASE}/api/session`, { method: 'POST' })
  if (!res.ok) throw new Error(`HTTP ${res.status}`)

  const data = (await res.json()) as { sessionId: string }
  await chrome.storage.session.set({ sessionId: data.sessionId })
  return { sessionId: data.sessionId }
}

// Validate that a session exists on the server, then store and return it.
// The URL parameter always wins over whatever was previously stored — the
// user explicitly followed a join link, so that intent takes priority.
async function joinSession(sessionId: string): Promise<BgResponse> {
  const res = await fetch(`${API_BASE}/api/session/${sessionId}`)
  if (!res.ok) throw new Error(`session ${sessionId} not found`)

  await chrome.storage.session.set({ sessionId })
  return { sessionId }
}
