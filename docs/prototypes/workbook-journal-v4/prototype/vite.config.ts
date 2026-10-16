// Dev server for the workbook-journal v4 prototype only (its own entry; the real app entry is untouched).
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import svgr from 'vite-plugin-svgr'

export default defineConfig({
  root: __dirname,
  base: './',
  plugins: [svgr(), react(), tailwindcss()],
})
