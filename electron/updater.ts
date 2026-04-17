import { app } from 'electron'
import { existsSync, mkdirSync, rmSync, renameSync, cpSync, createWriteStream } from 'fs'
import { join } from 'path'
import { pipeline } from 'stream/promises'
import { extract } from 'tar'

declare const __APP_VERSION__: string
declare const __ELECTRON_HASH__: string
declare const __SPA_HASH__: string

export interface AppInfo {
  version: string
  electronHash: string
  spaHash: string
  devUpdateEnabled: boolean
}

export interface RemoteVersionInfo {
  version: string
  spaHash: string
  electronHash: string
  source: { spaHash: string; electronHash: string }
  building: boolean
  buildError: string
  requiresFullRebuild: boolean
  fullRebuildReason?: string
}

export function getAppInfo(): AppInfo {
  return {
    version: typeof __APP_VERSION__ !== 'undefined' ? __APP_VERSION__ : 'unknown',
    electronHash: typeof __ELECTRON_HASH__ !== 'undefined' ? __ELECTRON_HASH__ : 'unknown',
    spaHash: typeof __SPA_HASH__ !== 'undefined' ? __SPA_HASH__ : 'unknown',
    devUpdateEnabled: !!process.env.PDX_DEV_UPDATE,
  }
}

function authHeaders(token?: string): Record<string, string> {
  return token ? { Authorization: `Bearer ${token}` } : {}
}

export async function checkUpdate(daemonUrl: string, token?: string): Promise<RemoteVersionInfo> {
  const resp = await fetch(`${daemonUrl}/api/dev/update/check`, { headers: authHeaders(token) })
  if (!resp.ok) throw new Error(`check failed: ${resp.status}`)
  return resp.json()
}

export interface StreamCheckEvent {
  type: 'check' | 'phase' | 'stdout' | 'stderr' | 'done' | 'error'
  phase?: string
  line?: string
  error?: string
  check?: RemoteVersionInfo
}

/**
 * Opens an SSE connection to /api/dev/update/check/stream and invokes
 * onEvent for every message until the server closes or the signal aborts.
 *
 * Event contract (matches internal/module/dev/handler.go):
 *   - first event is always { type: 'check', check: RemoteVersionInfo }
 *   - middle events carry build progress (phase / stdout / stderr)
 *   - a single terminal { type: 'done', check: RemoteVersionInfo } ends the stream
 */
export async function streamCheck(
  daemonUrl: string,
  token: string | undefined,
  onEvent: (ev: StreamCheckEvent) => void,
  signal?: AbortSignal,
): Promise<void> {
  const resp = await fetch(`${daemonUrl}/api/dev/update/check/stream`, {
    headers: { ...authHeaders(token), Accept: 'text/event-stream' },
    signal,
  })
  if (!resp.ok) throw new Error(`stream failed: ${resp.status}`)
  if (!resp.body) throw new Error('stream has no body')

  const reader = resp.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  try {
    while (true) {
      const { done, value } = await reader.read()
      if (done) return
      buffer += decoder.decode(value, { stream: true })
      let idx: number
      while ((idx = buffer.indexOf('\n\n')) >= 0) {
        const chunk = buffer.slice(0, idx)
        buffer = buffer.slice(idx + 2)
        for (const line of chunk.split('\n')) {
          if (!line.startsWith('data: ')) continue
          try {
            onEvent(JSON.parse(line.slice(6)))
          } catch {
            // malformed frame — skip, keep streaming
          }
        }
      }
    }
  } finally {
    try {
      reader.releaseLock()
    } catch {
      // reader already released
    }
  }
}

export type UpdateProgressFn = (step: string) => void

export async function applyUpdate(
  daemonUrl: string,
  onProgress?: UpdateProgressFn,
  token?: string,
): Promise<{ success: boolean; message: string }> {
  const progress = onProgress ?? (() => {})

  progress('downloading')
  const resp = await fetch(`${daemonUrl}/api/dev/update/download`, { headers: authHeaders(token) })
  if (!resp.ok) throw new Error(`download failed: ${resp.status}`)

  const tmpDir = join(app.getPath('temp'), 'purdex-update')
  if (existsSync(tmpDir)) rmSync(tmpDir, { recursive: true })
  mkdirSync(tmpDir, { recursive: true })

  // Save tar.gz to temp file
  const tarPath = join(tmpDir, 'out.tar.gz')
  const fileStream = createWriteStream(tarPath)
  await pipeline(resp.body as any, fileStream)

  progress('extracting')
  // Extract to temp dir
  const extractDir = join(tmpDir, 'extracted')
  mkdirSync(extractDir, { recursive: true })
  await extract({ file: tarPath, cwd: extractDir })

  progress('applying')

  // Replace out/main, out/preload, and out/renderer in app directory
  // __dirname is out/main/ in the built output, so parent is out/
  const appOutDir = join(__dirname, '..')
  const targets = ['main', 'preload', 'renderer'] as const

  // Backup current files before replacing
  const backupDir = join(tmpDir, 'backup')
  mkdirSync(backupDir, { recursive: true })
  for (const name of targets) {
    const dst = join(appOutDir, name)
    if (existsSync(dst)) cpSync(dst, join(backupDir, name), { recursive: true })
  }

  try {
    for (const name of targets) {
      const src = join(extractDir, name)
      const dst = join(appOutDir, name)
      if (existsSync(src)) {
        if (existsSync(dst)) rmSync(dst, { recursive: true })
        renameSync(src, dst)
      }
    }
  } catch (err) {
    // Rollback: restore from backup — each target independently so one
    // failure does not prevent restoring the others
    for (const name of targets) {
      try {
        const backup = join(backupDir, name)
        const dst = join(appOutDir, name)
        if (existsSync(backup)) {
          if (existsSync(dst)) rmSync(dst, { recursive: true })
          renameSync(backup, dst)
        }
      } catch (rollbackErr) {
        console.error(`[updater] rollback failed for ${name}:`, rollbackErr)
      }
    }
    try { rmSync(tmpDir, { recursive: true }) } catch { /* best effort */ }
    throw err
  }

  // Cleanup temp
  rmSync(tmpDir, { recursive: true })

  // Relaunch — no progress('restarting') here because app.exit(0)
  // kills the process before the IPC message reaches the renderer.
  app.relaunch()
  app.exit(0)

  return { success: true, message: 'Update applied, restarting...' }
}
