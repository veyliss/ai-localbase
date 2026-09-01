import path from 'node:path'
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

const proxyTarget = process.env.VITE_DEV_PROXY_TARGET || 'http://localhost:8080'
const backendProxy = {
  target: proxyTarget,
  changeOrigin: true,
  xfwd: true,
}

const normalizedModuleId = (id: string) => id.split(path.sep).join('/')

// Keep chart engines and layout plugins cacheable without pulling them into the
// chat entry. Mermaid still decides which diagram definition is loaded on demand.
const manualChunks = (id: string) => {
  const moduleId = normalizedModuleId(id)

  if (moduleId.includes('/node_modules/react/') || moduleId.includes('/node_modules/react-dom/')) {
    return 'react'
  }
  if (
    moduleId.includes('/node_modules/react-markdown/') ||
    moduleId.includes('/node_modules/remark-gfm/')
  ) {
    return 'markdown'
  }

  if (
    moduleId.includes('/node_modules/cytoscape-cose-bilkent/')
  ) {
    return 'diagram-cytoscape-cose'
  }
  if (moduleId.includes('/node_modules/cytoscape-fcose/')) {
    return 'diagram-cytoscape-fcose'
  }
  if (moduleId.includes('/node_modules/cytoscape/')) {
    return 'diagram-cytoscape'
  }

  return undefined
}

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [react()],
  build: {
    // The budget script enforces 500 KB for normal chunks. Mermaid's official
    // core and parser remain non-first-screen exceptions capped at 650 KB.
    chunkSizeWarningLimit: 650,
    rollupOptions: {
      output: {
        manualChunks,
      },
    },
  },
  server: {
    port: 3000,
    proxy: {
      '/api': backendProxy,
      '/v1': backendProxy,
      '^/(?:.+/)?mcp(?:/.*)?$': backendProxy,
      '/upload': backendProxy,
      '/health': backendProxy,
    }
  }
})
