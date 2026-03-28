import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { crx } from '@crxjs/vite-plugin'
import manifest from './manifest.json'

export default defineConfig({
  plugins: [
    react(),
    crx({ manifest }),
  ],
  build: {
    rollupOptions: {
      // Treat pdf-viewer.html as a separate Vite entry so its TypeScript
      // imports are compiled and bundled (crxjs only processes manifest entries).
      input: {
        'pdf-viewer': 'pdf-viewer.html',
      },
    },
  },
  // Prevent Vite from rewriting chrome:// URLs
  server: {
    port: 5173,
    hmr: { port: 5173 },
  },
})
