import { defineConfig } from 'electron-vite'
import { resolve } from 'path'
import { execSync } from 'child_process'
import { readFileSync, writeFileSync, mkdirSync } from 'fs'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

function gitHash(...paths: string[]): string {
  try {
    return execSync(`git log -1 --format=%h -- ${paths.join(' ')}`, { encoding: 'utf-8' }).trim()
  } catch {
    return 'unknown'
  }
}

function readVersion(): string {
  try {
    return readFileSync(resolve(__dirname, 'VERSION'), 'utf-8').trim()
  } catch {
    return 'unknown'
  }
}

function buildInfoPlugin() {
  return {
    name: 'write-build-info',
    closeBundle() {
      const info = {
        version: readVersion(),
        spaHash: gitHash('spa/'),
        electronHash: gitHash('electron/', 'electron.vite.config.ts'),
        builtAt: new Date().toISOString(),
      }
      const outDir = resolve(__dirname, 'out')
      mkdirSync(outDir, { recursive: true })
      writeFileSync(resolve(outDir, '.build-info.json'), JSON.stringify(info, null, 2) + '\n')
    },
  }
}

const buildDefines = {
  __APP_VERSION__: JSON.stringify(readVersion()),
  __ELECTRON_HASH__: JSON.stringify(gitHash('electron/', 'electron.vite.config.ts')),
  __SPA_HASH__: JSON.stringify(gitHash('spa/')),
}

export default defineConfig({
  main: {
    plugins: [buildInfoPlugin()],
    build: {
      outDir: 'out/main',
      rollupOptions: {
        input: { index: resolve(__dirname, 'electron/main.ts') },
        external: ['electron'],
      },
    },
    define: buildDefines,
  },
  preload: {
    build: {
      outDir: 'out/preload',
      // Preload must be CJS (.js) — Electron sandbox does not support ESM preloads
      rollupOptions: {
        input: { index: resolve(__dirname, 'electron/preload.ts') },
        external: ['electron'],
        output: { format: 'cjs', entryFileNames: '[name].js' },
      },
    },
  },
  renderer: {
    root: 'spa',
    plugins: [react(), tailwindcss()],
    build: {
      outDir: resolve(__dirname, 'out/renderer'),
      rollupOptions: {
        input: resolve(__dirname, 'spa/index.html'),
      },
    },
  },
})
