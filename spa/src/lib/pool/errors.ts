// spa/src/lib/pool/errors.ts — the one classifier of `/api/pool/*` failures (spec §10, plan "WWD-7 design" error table).
// A store owns its timers and clears them when hidden or unmounted; this only says what the answer means and what to do next.
export const ERROR_CODES = [
  'no_answer', 'wwd_unavailable', 'wwd_bad_response', 'busy', 'wwd_error', 'host_unreachable', 'cursor_ahead',
  'not_found', 'host_not_local', 'wwd_rejected', 'forbidden', 'bad_request', 'unknown',
] as const
export type PoolErrorCode = (typeof ERROR_CODES)[number]

export type PoolErrorAction =
  | 'next_source'   // the next daemon announcing the capability; none left → the fleet fallback / the empty state
  | 'retry_same'    // the same source again after 1–2 s (busyDelayMs)
  | 'backoff'       // the same source, 2, 4, 8 … 30 s (backoffMs)
  | 'reset_origin'  // drop that origin's jobs and cursor, read from 0
  | 'none'
  | 'no_chart'      // keep the gauges, no chart
  | 'revert'        // a change: the value goes back
  | 'revert_retry'  // a change: the value goes back, with a retry
  | 'read_only'     // the section turns read only
  | 'log'

export interface PoolError { code: PoolErrorCode; action: PoolErrorAction }

/** `read` is a GET; `change` is a PUT / POST / DELETE that alters a setting. */
export type PoolCallKind = 'read' | 'change'

const BY_STATUS: Record<number, PoolErrorCode> = {
  400: 'bad_request', 403: 'forbidden', 404: 'not_found', 409: 'cursor_ahead', 429: 'busy', 501: 'host_not_local', 502: 'wwd_error', 503: 'wwd_unavailable',
}

function codeOf(status: number | null, body: unknown): PoolErrorCode {
  if (status === null) return 'no_answer'
  const named = typeof body === 'object' && body !== null && !Array.isArray(body) ? (body as { error?: unknown }).error : undefined
  if (typeof named === 'string') return (ERROR_CODES as readonly string[]).includes(named) ? (named as PoolErrorCode) : 'unknown'
  return BY_STATUS[status] ?? 'unknown'
}

/** `status` is null when nothing answered (a network failure). */
export function classify(status: number | null, body: unknown, kind: PoolCallKind = 'read'): PoolError {
  const code = codeOf(status, body)
  const change = kind === 'change'
  switch (code) {
    case 'no_answer': return { code, action: 'next_source' }
    case 'wwd_unavailable': return { code, action: change ? 'revert_retry' : 'next_source' }
    case 'wwd_bad_response': return { code, action: 'next_source' }
    case 'busy': return { code, action: 'retry_same' }
    case 'wwd_error':
    case 'host_unreachable': return { code, action: change ? 'revert_retry' : 'backoff' }
    case 'cursor_ahead': return { code, action: 'reset_origin' }
    case 'not_found': return { code, action: 'none' }
    case 'host_not_local': return { code, action: 'no_chart' }
    case 'wwd_rejected': return { code, action: 'revert' }
    case 'forbidden': return { code, action: 'read_only' }
    default: return { code, action: 'log' }
  }
}

const BACKOFF_FIRST_MS = 2000
const BACKOFF_MAX_MS = 30000
/** The wait before retry number `failures` (0-based) of a failing host or origin. */
export const backoffMs = (failures: number): number => Math.min(BACKOFF_FIRST_MS * 2 ** failures, BACKOFF_MAX_MS)
/** 1–2 s with jitter; `rand` is Math.random(). */
export const busyDelayMs = (rand: number): number => 1000 + Math.floor(rand * 1000)
