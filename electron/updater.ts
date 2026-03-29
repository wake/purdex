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
}

export function getAppInfo(): AppInfo {
  return {
    version: typeof __APP_VERSION__ !== 'undefined' ? __APP_VERSION__ : 'unknown',
    electronHash: typeof __ELECTRON_HASH__ !== 'undefined' ? __ELECTRON_HASH__ : 'unknown',
    spaHash: typeof __SPA_HASH__ !== 'undefined' ? __SPA_HASH__ : 'unknown',
    devUpdateEnabled: !!process.env.TBOX_DEV_UPDATE,
  }
}

export async function checkUpdate(daemonUrl: string): Promise<RemoteVersionInfo> {
  const resp = await fetch(`${daemonUrl}/api/dev/update/check`)
  if (!resp.ok) throw new Error(`check failed: ${resp.status}`)
  return resp.json()
}

export type UpdateProgressFn = (step: string) => void

export async function applyUpdate(
  daemonUrl: string,
  onProgress?: UpdateProgressFn,
): Promise<{ success: boolean; message: string }> {
  const progress = onProgress ?? (() => {})

  progress('downloading')
  const resp = await fetch(`${daemonUrl}/api/dev/update/download`)
  if (!resp.ok) throw new Error(`download failed: ${resp.status}`)

  const tmpDir = join(app.getPath('temp'), 'tbox-update')
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
