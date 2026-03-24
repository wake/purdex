import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// Note: Production deployment requires SPA fallback.
// Nginx: try_files $uri /index.html;
// Caddy: try_files {path} /index.html
export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    proxy: {
      '/api': 'http://localhost:7860',
      '/ws': { target: 'ws://localhost:7860', ws: true },
    },
  },
  preview: {
    proxy: {
      '/api': 'http://localhost:7860',
      '/ws': { target: 'ws://localhost:7860', ws: true },
    },
  },
  test: {
    environment: 'jsdom',
    setupFiles: './src/test-setup.ts',
    passWithNoTests: true,
  },
})
