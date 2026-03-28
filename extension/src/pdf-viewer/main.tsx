import React from 'react'
import ReactDOM from 'react-dom/client'
// Official PDF.js stylesheet — required for the text layer's CSS custom property transforms
import 'pdfjs-dist/web/pdf_viewer.css'
import { PdfViewer } from './PdfViewer'
import { Sidebar } from '../content/Sidebar'
import styles from '../content/sidebar.css?inline'

// Mount the PDF viewer in the main body
ReactDOM.createRoot(document.getElementById('pdf-root')!).render(<PdfViewer />)

// Mount the Seminar sidebar in a shadow DOM exactly like the content script does,
// so all sidebar functionality (session, chat, highlights) works in the viewer.
const host = document.createElement('div')
host.id = 'seminar-host'
host.style.cssText = 'all: initial; position: fixed; top: 0; right: 0; z-index: 2147483647;'
document.body.appendChild(host)

const shadow = host.attachShadow({ mode: 'open' })
const styleEl = document.createElement('style')
styleEl.textContent = styles
shadow.appendChild(styleEl)

const mountPoint = document.createElement('div')
shadow.appendChild(mountPoint)

ReactDOM.createRoot(mountPoint).render(<Sidebar />)
