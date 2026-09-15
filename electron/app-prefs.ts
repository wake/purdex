// Main-process preferences (JSON file under app.getPath('userData')).
//
// These are settings the main process needs before any renderer exists —
// e.g. whether to create the tray in app.whenReady — so the file on disk is
// the single source of truth and the SPA only reads/writes it over IPC.
// Parsing is lenient: anything missing or malformed falls back to defaults,
// and only the exact expected type is accepted per field.
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { dirname } from 'node:path'

export interface AppPrefs {
  showTray: boolean
}

export const DEFAULT_APP_PREFS: AppPrefs = { showTray: true }

export function parseAppPrefs(text: string | null | undefined): AppPrefs {
  const prefs: AppPrefs = { ...DEFAULT_APP_PREFS }
  if (text == null || text.trim() === '') return prefs
  let doc: unknown
  try {
    doc = JSON.parse(text)
  } catch {
    return prefs
  }
  if (typeof doc !== 'object' || doc === null || Array.isArray(doc)) return prefs
  const rec = doc as Record<string, unknown>
  if (typeof rec.showTray === 'boolean') prefs.showTray = rec.showTray
  return prefs
}

export function serializeAppPrefs(p: AppPrefs): string {
  return JSON.stringify(p, null, 2) + '\n'
}

export function loadAppPrefs(path: string): AppPrefs {
  let text: string | null = null
  try {
    text = readFileSync(path, 'utf8')
  } catch {
    // Missing file, permission error, or a directory — all mean "no prefs yet".
    text = null
  }
  return parseAppPrefs(text)
}

export function saveAppPrefs(path: string, p: AppPrefs): void {
  mkdirSync(dirname(path), { recursive: true })
  writeFileSync(path, serializeAppPrefs(p), 'utf8')
}
