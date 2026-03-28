import type { BgRequest, BgResponse } from '../types'

const API_BASE = 'http://localhost:8080'

// Respond to GET_SESSION from the content script.
// Returns an existing session ID from storage, or creates a new one via REST.
chrome.runtime.onMessage.addListener(
  (msg: BgRequest, _sender, sendResponse: (r: BgResponse) => void) => {
    if (msg.type === 'GET_SESSION') {
      getOrCreateSession().then(sendResponse).catch(() =>
        sendResponse({ error: 'failed to get session' })
      )
      return true // keep the message channel open for async response
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
