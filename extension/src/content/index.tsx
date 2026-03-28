import React from 'react'
import ReactDOM from 'react-dom/client'
import { Sidebar } from './Sidebar'
import styles from './sidebar.css?inline'

// Guard against double-injection (e.g. from HMR or navigation)
if (!document.getElementById('seminar-host')) {
  const host = document.createElement('div')
  host.id = 'seminar-host'
  // Reset all inherited styles so the host element is invisible/inert
  host.style.cssText = 'all: initial; position: fixed; top: 0; right: 0; z-index: 2147483647;'
  document.body.appendChild(host)

  // Shadow DOM isolates our styles from the host page (and vice-versa)
  const shadow = host.attachShadow({ mode: 'open' })

  const styleEl = document.createElement('style')
  styleEl.textContent = styles
  shadow.appendChild(styleEl)

  const mountPoint = document.createElement('div')
  shadow.appendChild(mountPoint)

  ReactDOM.createRoot(mountPoint).render(<Sidebar />)
}
