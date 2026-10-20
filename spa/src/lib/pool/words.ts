// spa/src/lib/pool/words.ts — the daemon's codes turned into words. Keys only; the surfaces call `t`. A code the App does not
// know is shown as it is, never as an error (the daemon may be newer).
import type { PoolErrorCode } from './errors'
import type { FallbackReason } from './host-view'
import type { HeadroomReasons, PoolZone, Slots } from './types'

type T = (key: string, opts?: Record<string, string | number>) => string

/** Headroom reason codes (#3478, spec §3.2). `exception:<name>` is the one family: it carries the exception's name. */
export const REASON_CODES = ['worker_off', 'zone_warning', 'zone_danger', 'zone_unknown', 'owner_active', 'slots_full', 'gate_full', 'disk_low', 'unknown'] as const
const EXCEPTION_PREFIX = 'exception:'

export function reasonWord(code: string, t: T): string {
  if (code.startsWith(EXCEPTION_PREFIX)) return t('pool.reason.exception', { name: code.slice(EXCEPTION_PREFIX.length) })
  return (REASON_CODES as readonly string[]).includes(code) ? t(`pool.reason.${code}`) : code
}

/**
 * The lines that say why a class of work is at 0 (host card, host tab). A class at 0 shows its reasons when the daemon sent them; a
 * daemon that sends reasons but none for a zero (spec §3.2 says it names `unknown`) reads as unknown; a daemon that sends no reasons at
 * all (older than pool.assess.v1) gets nothing: the plain 0, no reason invented. The same words for both classes are one line, for both.
 */
export function headroomReasonLines(headroom: Slots | null, reasons: HeadroomReasons | null, t: T): string[] {
  if (!headroom || !reasons) return []
  const wordsOf = (n: number, list: string[]) => (n !== 0 ? null : list.length > 0 ? list.map((c) => reasonWord(c, t)).join(' · ') : reasonWord('unknown', t))
  const heavy = wordsOf(headroom.heavy, reasons.heavy)
  const light = wordsOf(headroom.light, reasons.light)
  const line = (cls: 'heavy' | 'light' | 'both', words: string) => t('pool.card.reason_line', { cls: t(`pool.card.class.${cls}`), reason: words })
  if (heavy !== null && heavy === light) return [line('both', heavy)]
  return [heavy !== null ? line('heavy', heavy) : null, light !== null ? line('light', light) : null].filter((w): w is string => w !== null)
}

/** What the cell ring's load score is driven by (`internal/fleet/load.go`). */
export const DRIVERS = ['cpu', 'mem', 'pressure', 'paging', 'disk'] as const

/** The key of a driver's word, or null for a driver this App does not know (show it as text). */
export const driverKey = (driver: string): string | null => ((DRIVERS as readonly string[]).includes(driver) ? `pool.driver.${driver}` : null)

/** `offline` is the App's own state for a host nothing reads; the other four are the daemon's. */
export const zoneKey = (zone: PoolZone | 'offline'): string => `fleet.zone.${zone}`

const ERROR_TEXT: Partial<Record<PoolErrorCode, string>> = {
  no_answer: 'pool.error.unavailable',
  wwd_unavailable: 'pool.error.unavailable',
  wwd_bad_response: 'pool.error.bad_response',
  busy: 'pool.error.busy',
  wwd_error: 'pool.error.host_failing',
  host_unreachable: 'pool.error.host_failing',
  not_found: 'pool.error.not_found',
  host_not_local: 'pool.error.host_not_local',
  forbidden: 'pool.error.forbidden',
  bad_request: 'pool.error.bad_request',
}

/** The key of an error's line, or null when the code has none (wwd_rejected shows wwd's own text; cursor_ahead is silent). */
export const errorTextKey = (code: PoolErrorCode): string | null => ERROR_TEXT[code] ?? null

/** The line a host shows while the pool does not read it but the fleet does: which of the two it is, and since when the pool last read it. */
export function fallbackWord(t: T, reason: FallbackReason | null, since: number | null): string {
  const key = `pool.card.fallback_${reason ?? 'unreachable'}`
  return since !== null ? t(key, { time: new Date(since).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) }) : t(`${key}_nosince`)
}
