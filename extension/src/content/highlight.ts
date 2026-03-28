/**
 * Visual highlight overlay system.
 *
 * Uses Range.getClientRects() to get per-line bounding boxes and positions
 * absolutely-placed divs over them. This works reliably in content scripts
 * where the CSS Custom Highlight API can misbehave due to isolated worlds.
 */

import type { RangeData } from '../types'

// ── Color palette ─────────────────────────────────────────────────────────────

const COLORS = [
  'rgba(250, 204,  21, 0.45)', // yellow
  'rgba( 74, 222, 128, 0.45)', // green
  'rgba( 96, 165, 250, 0.45)', // blue
  'rgba(251, 113, 133, 0.45)', // red
  'rgba(192, 132, 252, 0.45)', // purple
  'rgba( 45, 212, 191, 0.45)', // teal
]

const clientColorIdx = new Map<string, number>()
let nextIdx = 0

function colorFor(clientId: string): string {
  if (!clientColorIdx.has(clientId)) {
    clientColorIdx.set(clientId, nextIdx % COLORS.length)
    nextIdx++
  }
  return COLORS[clientColorIdx.get(clientId)!]
}

// ── Public API ────────────────────────────────────────────────────────────────

/** Serialize the active Selection into wire-safe RangeData. Returns null on failure. */
export function serializeSelection(sel: Selection): RangeData | null {
  if (sel.rangeCount === 0) return null
  const range = sel.getRangeAt(0)
  try {
    return {
      startXPath: xpathFor(range.startContainer),
      startOffset: range.startOffset,
      endXPath: xpathFor(range.endContainer),
      endOffset: range.endOffset,
    }
  } catch {
    return null
  }
}

/** Overlay a peer's highlight onto the page. Replaces any previous overlay for that client. */
export function applyHighlight(clientId: string, data: RangeData): void {
  clearHighlight(clientId) // remove stale overlay first

  const range = rangeFrom(data)
  if (!range) return

  const color = colorFor(clientId)
  const rects = Array.from(range.getClientRects())
  if (rects.length === 0) return

  const container = document.createElement('div')
  container.id = highlightId(clientId)
  container.style.cssText = 'position:absolute;top:0;left:0;pointer-events:none;z-index:2147483646;'

  const scrollX = window.scrollX
  const scrollY = window.scrollY

  for (const rect of rects) {
    if (rect.width === 0 || rect.height === 0) continue
    const div = document.createElement('div')
    div.style.cssText = [
      'position:absolute',
      `top:${rect.top + scrollY}px`,
      `left:${rect.left + scrollX}px`,
      `width:${rect.width}px`,
      `height:${rect.height}px`,
      `background-color:${color}`,
      'mix-blend-mode:multiply', // blends over text like a real highlighter
      'border-radius:2px',
      'pointer-events:none',
    ].join(';')
    container.appendChild(div)
  }

  document.body.appendChild(container)
}

/** Remove a peer's highlight overlay (on leave or when they make a new selection). */
export function clearHighlight(clientId: string): void {
  document.getElementById(highlightId(clientId))?.remove()
}

// ── XPath helpers ─────────────────────────────────────────────────────────────

function xpathFor(node: Node): string {
  if (node === document.body) return '/html/body'
  if (node === document.documentElement) return '/html'

  if (node.nodeType === Node.TEXT_NODE) {
    const parent = node.parentElement
    if (!parent) throw new Error('orphaned text node')
    const textSiblings = Array.from(parent.childNodes).filter(
      n => n.nodeType === Node.TEXT_NODE
    )
    const idx = textSiblings.indexOf(node as Text) + 1
    return `${xpathFor(parent)}/text()[${idx}]`
  }

  if (node.nodeType === Node.ELEMENT_NODE) {
    const el = node as Element
    const parent = el.parentElement
    if (!parent) return `/${el.tagName.toLowerCase()}`
    const sameSiblings = Array.from(parent.children).filter(
      s => s.tagName === el.tagName
    )
    const idx = sameSiblings.indexOf(el) + 1
    const position = sameSiblings.length > 1 ? `[${idx}]` : ''
    return `${xpathFor(parent)}/${el.tagName.toLowerCase()}${position}`
  }

  throw new Error(`unsupported node type: ${node.nodeType}`)
}

function resolveXPath(xpath: string): Node | null {
  try {
    const result = document.evaluate(
      xpath, document, null, XPathResult.FIRST_ORDERED_NODE_TYPE, null
    )
    return result.singleNodeValue
  } catch {
    return null
  }
}

function rangeFrom(data: RangeData): Range | null {
  try {
    const startNode = resolveXPath(data.startXPath)
    const endNode = resolveXPath(data.endXPath)
    if (!startNode || !endNode) return null

    const range = document.createRange()
    range.setStart(startNode, data.startOffset)
    range.setEnd(endNode, data.endOffset)
    return range
  } catch {
    return null
  }
}

function highlightId(clientId: string): string {
  return `seminar-highlight-${clientId}`
}
