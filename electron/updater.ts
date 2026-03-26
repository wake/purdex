import { app } from 'electron'
import { existsSync, mkdirSync, rmSync, renameSync, createWriteStream } from 'fs'
import { join } from 'path'
import { pipeline } from 'stream/promises'
import { createGunzip } from 'zlib'
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

export async function applyUpdate(daemonUrl: string): Promise<{ success: boolean; message: string }> {
  const resp = await fetch(`${daemonUrl}/api/dev/update/download`)
  if (!resp.ok) throw new Error(`download failed: ${resp.status}`)

  const tmpDir = join(app.getPath('temp'), 'tbox-update')
  if (existsSync(tmpDir)) rmSync(tmpDir, { recursive: true })
  mkdirSync(tmpDir, { recursive: true })

  // Save tar.gz to temp file
  const tarPath = join(tmpDir, 'out.tar.gz')
  const fileStream = createWriteStream(tarPath)
  await pipeline(resp.body as any, fileStream)

  // Extract to temp dir
  const extractDir = join(tmpDir, 'extracted')
  mkdirSync(extractDir, { recursive: true })
  await extract({ file: tarPath, cwd: extractDir })

  // Replace out/main and out/preload in app directory
  // __dirname is out/main/ in the built output, so parent is out/
  const appOutDir = join(__dirname, '..')
  const mainDst = join(appOutDir, 'main')
  const preloadDst = join(appOutDir, 'preload')
  const mainSrc = join(extractDir, 'main')
  const preloadSrc = join(extractDir, 'preload')

  if (existsSync(mainSrc)) {
    if (existsSync(mainDst)) rmSync(mainDst, { recursive: true })
    renameSync(mainSrc, mainDst)
  }
  if (existsSync(preloadSrc)) {
    if (existsSync(preloadDst)) rmSync(preloadDst, { recursive: true })
    renameSync(preloadSrc, preloadDst)
  }

  // Cleanup temp
  rmSync(tmpDir, { recursive: true })

  // Relaunch
  app.relaunch()
  app.exit(0)

  return { success: true, message: 'Update applied, restarting...' }
}
