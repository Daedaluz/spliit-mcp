import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// In dev the SPA runs on :5173 and proxies the API and auth routes to the Go
// backend, so cookies and the OIDC redirect behave as they do in production.
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://localhost:8080',
      '/auth': 'http://localhost:8080',
      '/healthz': 'http://localhost:8080',
    },
  },
})
