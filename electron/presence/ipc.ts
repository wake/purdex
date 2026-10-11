// Presence IPC: validation and handlers (PA-2 D3, D6, D7). No `electron` import — the real
// `ipcMain` is injected, so everything here is unit-testable.
//
// Validation rejects with a fixed reason string; a reason never carries a value from the payload
// (the payload holds tokens). Tokens are never logged and never returned to a renderer.
import { AWAY_MINUTES_OPTIONS, isAwayMinutes } from '../app-prefs'
import type { HostsPayload } from './hosts'

export const MAX_HOSTS = 64
export const MAX_PAYLOAD_BYTES = 256 * 1024
const MAX_HOST_ID = 128
const MAX_TOKEN = 4096
const MAX_URL = 2048

export type Validation = { ok: true; value: HostsPayload } | { ok: false; reason: string }

const fail = (reason: string): Validation => ({ ok: false, reason })

const isPlainObject = (v: unknown): v is Record<string, unknown> => {
  if (typeof v !== 'object' || v === null || Array.isArray(v)) return false
  const proto = Object.getPrototypeOf(v)
  return proto === Object.prototype || proto === null
}

/** Origin of an http(s) URL that has no userinfo, path, query or hash; null otherwise. */
function originOnly(raw: string): string | null {
  if (raw.length === 0 || raw.length > MAX_URL) return null
  // A bare "?" or "#" leaves `search` / `hash` empty after parsing, so look at the raw text too.
  if (raw.includes('?') || raw.includes('#')) return null
  let u: URL
  try {
    u = new URL(raw)
  } catch {
    return null
  }
  if (u.protocol !== 'http:' && u.protocol !== 'https:') return null
  if (u.username !== '' || u.password !== '') return null
  if (u.pathname !== '/' || u.search !== '' || u.hash !== '') return null
  if (u.hostname === '') return null
  return u.origin
}

export function validateHostsPayload(raw: unknown): Validation {
  if (!isPlainObject(raw)) return fail('payload: want an object')
  const { rev, hosts } = raw
  if (typeof rev !== 'number' || !Number.isSafeInteger(rev) || rev < 0) return fail('rev: want a non-negative safe integer')
  if (!Array.isArray(hosts)) return fail('hosts: want an array')
  if (hosts.length > MAX_HOSTS) return fail('hosts: too many')
  const out: HostsPayload['hosts'] = []
  const seen = new Set<string>()
  for (const entry of hosts) {
    if (!isPlainObject(entry)) return fail('host: want an object')
    const { hostId, baseUrl, token } = entry
    if (typeof hostId !== 'string' || hostId.length === 0 || hostId.length > MAX_HOST_ID) return fail('hostId: want 1-128 chars')
    if (seen.has(hostId)) return fail('hostId: duplicate')
    seen.add(hostId)
    if (typeof baseUrl !== 'string') return fail('baseUrl: want a string')
    const origin = originOnly(baseUrl)
    if (origin === null) return fail('baseUrl: want an http(s) origin without userinfo, path, query or hash')
    if (token !== null && (typeof token !== 'string' || token.length > MAX_TOKEN)) return fail('token: want null or a string up to 4096 chars')
    out.push({ hostId, baseUrl: origin, token })
  }
  let size: number
  try {
    size = JSON.stringify(raw).length
  } catch {
    return fail('payload: cannot be serialised')
  }
  if (size > MAX_PAYLOAD_BYTES) return fail('payload: too large')
  return { ok: true, value: { rev, hosts: out } }
}

// ---- away minutes (D6) ----
export interface AwayMinutesStore {
  get(): number
  /** Throws for a value outside the offered set or when saving fails; returns the saved value. */
  set(value: unknown): number
}

export function createAwayMinutesStore(io: { load: () => number; save: (n: number) => void }): AwayMinutesStore {
  let current: number | null = null
  return {
    get() {
      if (current === null) current = io.load()
      return current
    },
    set(value) {
      if (!isAwayMinutes(value)) throw new Error(`presence: away minutes must be one of ${AWAY_MINUTES_OPTIONS.join(', ')}`)
      io.save(value)
      current = value
      return value
    },
  }
}

// ---- handlers ----
export interface IpcMainLike {
  handle(channel: string, listener: (event: unknown, ...args: unknown[]) => unknown): void
}

export interface PresenceIpcDeps {
  ipcMain: IpcMainLike
  reporter: {
    setHosts: (payload: HostsPayload) => unknown
    getOwned: () => string[]
    force: (reason: 'n') => void
  }
  minutes: AwayMinutesStore
  logger: { warn: (message: string) => void }
}

export function registerPresenceIpc(deps: PresenceIpcDeps): void {
  const { ipcMain, reporter, minutes, logger } = deps

  ipcMain.handle('presence:set-hosts', (_event, payload) => {
    const v = validateHostsPayload(payload)
    if (!v.ok) {
      logger.warn(`presence: set-hosts rejected (${v.reason})`)
      return false
    }
    reporter.setHosts(v.value)
    return true
  })

  ipcMain.handle('presence:get-away-minutes', () => minutes.get())

  ipcMain.handle('presence:set-away-minutes', (_event, value) => {
    const saved = minutes.set(value)
    reporter.force('n')
    return saved
  })

  ipcMain.handle('presence:get-owned', () => reporter.getOwned())
}
