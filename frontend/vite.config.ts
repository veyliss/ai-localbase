import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

const proxyTarget = process.env.VITE_DEV_PROXY_TARGET || 'http://localhost:8080'
const backendProxy = {
  target: proxyTarget,
  changeOrigin: true,
  xfwd: true,
}

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [react()],
  build: {
    rollupOptions: {
      output: {
        manualChunks: {
          react: ['react', 'react-dom'],
          markdown: ['react-markdown', 'remark-gfm'],
        },
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
