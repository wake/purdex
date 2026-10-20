// spa/src/lib/pool/words.ts — the daemon's codes turned into words. Keys only; the surfaces call `t`. A code the App does not
// know is shown as it is, never as an error (the daemon may be newer).
import type { PoolErrorCode } from './errors'
import type { FallbackReason } from './host-view'
import type { PoolHostState } from './host-state'
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

/** The state's short word: `pool.liveness.<ok | wwd_down | host_down | unknown>` (the card's headline and the accessible name). */
export const livenessWord = (t: T, s: PoolHostState): string => t(`pool.liveness.${s.state}`)

const clock = (ms: number): string => new Date(ms).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })

/** "N seconds / minutes / hours ago", whole units rounded down. */
function agoWord(t: T, ms: number): string {
  const sec = Math.max(0, Math.floor(ms / 1000))
  if (sec < 60) return t('pool.liveness.ago_s', { n: sec })
  if (sec < 3600) return t('pool.liveness.ago_m', { n: Math.floor(sec / 60) })
  return t('pool.liveness.ago_h', { n: Math.floor(sec / 3600) })
}

/** M25: the card of a host in a pool nobody can read: why the pool cannot be read (the failure's word, or that no daemon is connected), then the last reading of this host. */
export function poolOutageLines(t: T, failure: { code: PoolErrorCode | null }, lastFactsAt: number | null, now: number): string[] {
  const key = failure.code === null ? null : errorTextKey(failure.code)
  const reason = key === null || key === 'pool.error.unavailable' ? t('pool.error.unavailable_none') : t(key)
  const lines = [t('pool.card.unreadable', { reason })]
  lines.push(lastFactsAt !== null ? t('pool.liveness.last_reading', { time: clock(lastFactsAt), ago: agoWord(t, now - lastFactsAt) }) : t('pool.liveness.no_reading'))
  return lines
}

/**
 * The lines under a host card's headline (M11, M17–M21, W3): what the state means, the last reading and how long ago, the last check and the
 * 5 s rhythm. An ok host says one word (no last check, M20); the local host, which has no probe of itself, says it is the local wwd.
 */
export function livenessLines(t: T, s: PoolHostState, now: number): string[] {
  if (s.state === 'ok') return [t(s.pool.liveness === undefined ? 'pool.liveness.ok_local' : 'pool.liveness.ok')]
  const lines: string[] = []
  if (s.stalled) {
    lines.push(s.lastFactsAt !== null ? t('pool.liveness.explain_stalled', { time: clock(s.lastFactsAt) }) : t('pool.liveness.explain_stalled_none'))
  } else {
    lines.push(t(`pool.liveness.explain_${s.state}`))
    lines.push(s.lastFactsAt !== null ? t('pool.liveness.last_reading', { time: clock(s.lastFactsAt), ago: agoWord(t, now - s.lastFactsAt) }) : t('pool.liveness.no_reading'))
  }
  if (s.checkedAt !== null) lines.push(t('pool.liveness.checked', { time: clock(s.checkedAt) }))
  return lines
}
