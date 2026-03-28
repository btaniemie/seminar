/**
 * Visual highlight overlay system.
 *
 * Uses Range.getClientRects() to get per-line bounding boxes and positions
 * absolutely-placed divs over them. This works reliably in content scripts
 * where the CSS Custom Highlight API can misbehave due to isolated worlds.
 */

import type { RangeData } from '../types'

// ── Helpers ───────────────────────────────────────────────────────────────────

/** Convert a #RRGGBB hex color to rgba(...) with the given alpha. */
function hexToRgba(hex: string, alpha: number): string {
  const r = parseInt(hex.slice(1, 3), 16)
  const g = parseInt(hex.slice(3, 5), 16)
  const b = parseInt(hex.slice(5, 7), 16)
  return `rgba(${r},${g},${b},${alpha})`
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

/**
 * Overlay a peer's highlight onto the page with an initials label.
 * Replaces any previous overlay for that client.
 *
 * @param initials - 1–2 uppercase letters shown in a pill above the selection
 * @param color    - hex color (#RRGGBB) matching the user's presence avatar
 */
export function applyHighlight(
  clientId: string,
  data: RangeData,
  initials: string,
  color: string,
): void {
  clearHighlight(clientId) // remove stale overlay first

  const range = rangeFrom(data)
  if (!range) return

  const bgColor = hexToRgba(color, 0.4)
  const rects = Array.from(range.getClientRects()).filter(r => r.width > 0 && r.height > 0)
  if (rects.length === 0) return

  const container = document.createElement('div')
  container.id = highlightId(clientId)
  container.style.cssText = 'position:absolute;top:0;left:0;pointer-events:none;z-index:2147483646;'

  const scrollX = window.scrollX
  const scrollY = window.scrollY

  // Name label — sits just above the first line of the selection
  if (initials) {
    const label = document.createElement('div')
    const firstRect = rects[0]
    label.style.cssText = [
      'position:absolute',
      `top:${firstRect.top + scrollY - 19}px`,
      `left:${firstRect.left + scrollX}px`,
      `background-color:${color}`,
      'color:#18181B',
      'font-family:system-ui,-apple-system,sans-serif',
      'font-size:10px',
      'font-weight:700',
      'padding:1px 6px',
      'border-radius:3px',
      'line-height:16px',
      'pointer-events:none',
      'white-space:nowrap',
      'letter-spacing:0.04em',
    ].join(';')
    label.textContent = initials
    container.appendChild(label)
  }

  for (const rect of rects) {
    const div = document.createElement('div')
    div.style.cssText = [
      'position:absolute',
      `top:${rect.top + scrollY}px`,
      `left:${rect.left + scrollX}px`,
      `width:${rect.width}px`,
      `height:${rect.height}px`,
      `background-color:${bgColor}`,
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
