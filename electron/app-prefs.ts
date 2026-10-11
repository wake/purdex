// Main-process preferences (JSON file under app.getPath('userData')).
//
// These are settings the main process needs before any renderer exists —
// e.g. whether to create the tray in app.whenReady — so the file on disk is
// the single source of truth and the SPA only reads/writes it over IPC.
// Parsing is lenient: anything missing or malformed falls back to defaults,
// and only the exact expected type is accepted per field.
import { randomUUID } from 'node:crypto'
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { dirname } from 'node:path'

/** Minutes away from the computer before a push goes to the phone (PA-2). */
export const AWAY_MINUTES_OPTIONS = [1, 2, 3, 5, 10, 15, 30, 60] as const
export const DEFAULT_AWAY_MINUTES = 5

export interface AppPrefs {
  showTray: boolean
  /** One of AWAY_MINUTES_OPTIONS. */
  presenceAwayMinutes: number
  /** Random id of this App install (UUID), null until first needed. Not a secret, never a credential. */
  installId: string | null
}

export const DEFAULT_APP_PREFS: AppPrefs = {
  showTray: true,
  presenceAwayMinutes: DEFAULT_AWAY_MINUTES,
  installId: null,
}

const INSTALL_ID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

export function isAwayMinutes(v: unknown): v is number {
  return typeof v === 'number' && (AWAY_MINUTES_OPTIONS as readonly number[]).includes(v)
}

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
  if (isAwayMinutes(rec.presenceAwayMinutes)) prefs.presenceAwayMinutes = rec.presenceAwayMinutes
  if (typeof rec.installId === 'string' && INSTALL_ID_RE.test(rec.installId)) prefs.installId = rec.installId
  return prefs
}

export function serializeAppPrefs(p: AppPrefs): string {
  // Explicit field list: nothing but the known keys is ever written.
  const out: AppPrefs = {
    showTray: p.showTray,
    presenceAwayMinutes: p.presenceAwayMinutes,
    installId: p.installId,
  }
  return JSON.stringify(out, null, 2) + '\n'
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

/**
 * The install id of this App, generated once (first need) and saved. An invalid stored value is
 * replaced. Throws if the prefs file cannot be written, so the caller never uses an id that
 * would differ on the next launch.
 */
export function ensureInstallId(path: string, generate: () => string = randomUUID): string {
  const prefs = loadAppPrefs(path)
  if (prefs.installId) return prefs.installId
  const id = generate()
  saveAppPrefs(path, { ...prefs, installId: id })
  return id
}
