// spa/src/lib/watch/api.ts — `GET /api/watch/status` and `/events`, through the same host fetch helper as lib/fleet/api.ts.
import { hostFetch } from '../host-api'
import { parseWatchStatus, type WatchStatus } from './status-types'
import { isCount, parseWatchEvents, type WatchEvent } from './events-types'
import { HISTORY_LIMIT } from './history-view'
import { parseGateDecisions, type GateDecision } from './decisions-types'

export type GateDecisionsResult =
  | { kind: 'ok'; records: GateDecision[]; skipped: number }
  | { kind: 'forbidden' } // 403: the host's token is not an admin token
  | { kind: 'server' } // any other answer that is not a readable list (500: the file exists but cannot be read)
  | { kind: 'network' }

/** `GET /api/gate/decisions` (newest first; 1000 is the daemon's largest page) for one class since `sinceIso`. */
export async function fetchGateDecisions(hostId: string, cls: 'heavy' | 'light', sinceIso: string): Promise<GateDecisionsResult> {
  try {
    const res = await hostFetch(hostId, `/api/gate/decisions?class=${cls}&since=${encodeURIComponent(sinceIso)}&limit=${HISTORY_LIMIT}`)
    if (res.status === 403) return { kind: 'forbidden' }
    if (!res.ok) return { kind: 'server' }
    const body = parseGateDecisions(await res.json())
    return body ? { kind: 'ok', ...body } : { kind: 'server' }
  } catch {
    return { kind: 'network' }
  }
}

export type WatchStatusResult = { kind: 'ok'; status: WatchStatus } | { kind: 'unsupported' } | { kind: 'error' }

/** 404 → `unsupported` (a daemon without the watch module); an unreadable body, an unknown state or any other failure → `error`. */
export async function fetchWatchStatus(hostId: string): Promise<WatchStatusResult> {
  try {
    const res = await hostFetch(hostId, '/api/watch/status')
    if (res.status === 404) return { kind: 'unsupported' }
    if (!res.ok) return { kind: 'error' }
    const status = parseWatchStatus(await res.json())
    return status ? { kind: 'ok', status } : { kind: 'error' }
  } catch {
    return { kind: 'error' }
  }
}

export type WatchUnhandledResult = { kind: 'ok'; count: number } | { kind: 'unsupported' } | { kind: 'error' }

/** `GET /api/watch/unhandled` → `{count}`: how many events this host's watcher has not handled (all of them, not one page's). */
export async function fetchWatchUnhandled(hostId: string): Promise<WatchUnhandledResult> {
  try {
    const res = await hostFetch(hostId, '/api/watch/unhandled')
    if (res.status === 404) return { kind: 'unsupported' }
    if (!res.ok) return { kind: 'error' }
    const body: unknown = await res.json()
    const count = typeof body === 'object' && body !== null ? (body as { count?: unknown }).count : undefined
    return isCount(count) ? { kind: 'ok', count } : { kind: 'error' }
  } catch {
    return { kind: 'error' }
  }
}

export type WatchEventsResult = { kind: 'ok'; events: WatchEvent[] } | { kind: 'unsupported' } | { kind: 'error' }

/** `GET /api/watch/events` (newest first; 500 is the daemon's largest page). 404 → `unsupported`. */
export async function fetchWatchEvents(hostId: string): Promise<WatchEventsResult> {
  try {
    const res = await hostFetch(hostId, '/api/watch/events?limit=500')
    if (res.status === 404) return { kind: 'unsupported' }
    if (!res.ok) return { kind: 'error' }
    const events = parseWatchEvents(await res.json())
    return events ? { kind: 'ok', events } : { kind: 'error' }
  } catch {
    return { kind: 'error' }
  }
}
