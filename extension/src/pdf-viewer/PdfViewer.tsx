import React, { useEffect, useRef, useState } from 'react'
// Import PDF.js from the locally bundled npm package — no remote scripts needed,
// which satisfies MV3's strict extension_pages CSP.
import { getDocument, GlobalWorkerOptions, TextLayer } from 'pdfjs-dist'
// Vite resolves this to the compiled worker asset path at build time.
import pdfjsWorker from 'pdfjs-dist/build/pdf.worker.min.mjs?url'

GlobalWorkerOptions.workerSrc = pdfjsWorker

export function PdfViewer() {
  const fileUrl = new URLSearchParams(window.location.search).get('file') ?? ''
  const containerRef = useRef<HTMLDivElement>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [numPages, setNumPages] = useState(0)

  useEffect(() => {
    if (!fileUrl) {
      setError('No PDF URL provided.')
      setLoading(false)
      return
    }
    loadPdf()
  }, [fileUrl])

  async function loadPdf() {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    let pdf: any
    try {
      pdf = await getDocument({ url: fileUrl, withCredentials: false }).promise
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : String(e)
      if (msg.includes('CORS') || msg.includes('fetch') || msg.includes('NetworkError')) {
        setError(
          'This PDF is on a different origin and cannot be loaded directly. ' +
          'Try downloading the PDF and opening it as a local file.'
        )
      } else {
        setError(`Could not load PDF: ${msg}`)
      }
      setLoading(false)
      return
    }

    setNumPages(pdf.numPages)
    setLoading(false)

    const container = containerRef.current
    if (!container) return

    const totalPages = pdf.numPages
    for (let pageNum = 1; pageNum <= totalPages; pageNum++) {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      const page = await (pdf as any).getPage(pageNum)
      const viewport = page.getViewport({ scale: 1.4 })

      const pageDiv = document.createElement('div')
      pageDiv.style.cssText = [
        'position: relative',
        `width: ${viewport.width}px`,
        `height: ${viewport.height}px`,
        'margin: 0 auto 24px',
        'box-shadow: 0 2px 12px rgba(0,0,0,.15)',
        'background: #fff',
        'flex-shrink: 0',
      ].join(';')
      container.appendChild(pageDiv)

      const canvas = document.createElement('canvas')
      canvas.width  = viewport.width
      canvas.height = viewport.height
      canvas.style.cssText = 'position: absolute; top: 0; left: 0;'
      pageDiv.appendChild(canvas)

      await page.render({ canvasContext: canvas.getContext('2d')!, viewport }).promise

      // Text layer — transparent spans over the canvas so text is selectable
      // pdf_viewer.css positions and styles the text layer via CSS custom properties
      // that TextLayer.render() sets at runtime (--total-scale-factor etc.)
      const textLayerDiv = document.createElement('div')
      textLayerDiv.className = 'textLayer'
      pageDiv.appendChild(textLayerDiv)

      // PDF.js v5 uses the TextLayer class instead of renderTextLayer
      const textLayer = new TextLayer({
        textContentSource: page.streamTextContent(),
        container: textLayerDiv,
        viewport,
      })
      await textLayer.render()
    }
  }

  return (
    <div style={{ display: 'flex', height: '100vh', background: '#3D3D3D', overflow: 'hidden' }}>
      {/* PDF scroll area — leaves 320 px on the right for the sidebar.
          id="seminar-pdf-scroll" + position:relative lets highlight.ts
          append overlays here so they scroll with the PDF content. */}
      <div
        id="seminar-pdf-scroll"
        style={{
          flex: 1,
          overflowY: 'auto',
          padding: '24px',
          marginRight: '320px',
          display: 'flex',
          flexDirection: 'column',
          alignItems: 'center',
          position: 'relative',
        }}
      >
        {/* URL bar */}
        <div style={{
          width: '100%', maxWidth: 900,
          background: '#2A2A2A', borderRadius: 6,
          padding: '6px 12px', marginBottom: 16,
          color: '#A8A29E', fontSize: 11,
          fontFamily: 'ui-monospace, monospace',
          overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
        }}>
          {fileUrl}
        </div>

        {loading && (
          <p style={{ color: '#E7E5E0', fontFamily: 'system-ui', marginTop: 40 }}>
            Loading PDF…
          </p>
        )}

        {error && (
          <div style={{
            background: '#2A2A2A', borderRadius: 8, padding: 24,
            color: '#F87171', fontFamily: 'system-ui', fontSize: 14,
            maxWidth: 500, marginTop: 40, lineHeight: 1.6,
          }}>
            <strong>Could not load PDF</strong><br />{error}
          </div>
        )}

        {!loading && !error && numPages > 0 && (
          <p style={{
            color: '#A8A29E', fontSize: 11, fontFamily: 'system-ui',
            marginBottom: 16, alignSelf: 'flex-start', maxWidth: 900, width: '100%',
          }}>
            {numPages} page{numPages !== 1 ? 's' : ''} — select text to highlight
          </p>
        )}

        <div ref={containerRef} style={{ width: '100%', maxWidth: 900 }} />
      </div>
    </div>
  )
}
