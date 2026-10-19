// spa/src/lib/pool/words.ts — the daemon's codes turned into words. Keys only; the surfaces call `t`. A code the App does not
// know is shown as it is, never as an error (the daemon may be newer).
import type { PoolErrorCode } from './errors'
import type { PoolZone } from './types'

type T = (key: string, opts?: Record<string, unknown>) => string

/** Headroom reason codes (#3478, spec §3.2). `exception:<name>` is the one family: it carries the exception's name. */
export const REASON_CODES = ['worker_off', 'zone_warning', 'zone_danger', 'zone_unknown', 'owner_active', 'slots_full', 'gate_full'] as const
const EXCEPTION_PREFIX = 'exception:'

export function reasonWord(code: string, t: T): string {
  if (code.startsWith(EXCEPTION_PREFIX)) return t('pool.reason.exception', { name: code.slice(EXCEPTION_PREFIX.length) })
  return (REASON_CODES as readonly string[]).includes(code) ? t(`pool.reason.${code}`) : code
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
