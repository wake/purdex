// spa/src/lib/notification-dedup.ts — the persistent (localStorage) dedup of agent notifications, by broadcast stamp or
// by a worker's request id, and that request's Electron stamp; moved from hooks/useNotificationDispatcher.ts (#1690).
import type { NormalizedEvent } from '../stores/useAgentStore'
import { STORAGE_KEYS } from './storage'

/** Check if a notification should be dispatched based on broadcast_ts dedup.
 *  New sessions default to Infinity (sentinel), so their first event is recorded
 *  but not dispatched — prevents snapshot flooding on new/restarted clients. */
export function shouldDispatch(sessionCode: string, broadcastTs: number): boolean {
  const data: Record<string, number> = JSON.parse(localStorage.getItem(STORAGE_KEYS.NOTIFICATION_SEEN) || '{}')
  const stored = data[sessionCode] ?? Infinity

  if (broadcastTs <= stored) {
    if (stored === Infinity) {
      data[sessionCode] = broadcastTs
      localStorage.setItem(STORAGE_KEYS.NOTIFICATION_SEEN, JSON.stringify(data))
    }
    return false
  }

  data[sessionCode] = broadcastTs
  localStorage.setItem(STORAGE_KEYS.NOTIFICATION_SEEN, JSON.stringify(data))
  return true
}

/** Request ids remembered per key. A worker's requests are serialized, so these are the last ones it asked. */
const SEEN_REQUESTS_CAP = 20

/** The request an event is about: a worker awaiting approval carries its pending request's id (useWorkerAgentProjection). */
export function requestIdOf(event: NormalizedEvent | undefined): string | null {
  const id = event?.detail?.request_id
  return typeof id === 'string' && id !== '' ? id : null
}

function readSeenRequests(): Record<string, string[]> {
  try {
    const raw: unknown = JSON.parse(localStorage.getItem(STORAGE_KEYS.NOTIFICATION_SEEN_REQUESTS) || '{}')
    const out: Record<string, string[]> = {}
    if (raw === null || typeof raw !== 'object' || Array.isArray(raw)) return out
    for (const [key, ids] of Object.entries(raw)) {
      if (Array.isArray(ids)) out[key] = ids.filter((id): id is string => typeof id === 'string')
    }
    return out
  } catch {
    return {} // a corrupt map is read as empty
  }
}

/**
 * Persistent dedup for an event about one request — a worker awaiting approval, which notifies like an agent ask once
 * per request (permission channel PC2 as amended 2026-10-07). It goes by the request's id, not by stamp: Nexen
 * serializes a worker's requests but two can still share the millisecond their `since` (the event's stamp) is taken
 * in, and `shouldDispatch` would drop the second. The baseline is an agent ask's:
 * - a key with no entry in either map (never seen on this client) records the id and stays quiet — the Infinity
 *   sentinel of `shouldDispatch`, so a first snapshot never floods;
 * - a known key and an id not seen yet dispatches once — including a request that came while the App was closed;
 * - an id seen before never dispatches again (refetch, reconnect, list row <-> live summary, tab switch, reload).
 * The stamp map still keeps the newest stamp, so the key's other events (Stop, …) keep their `shouldDispatch` dedup.
 */
export function shouldDispatchRequest(ck: string, requestId: string, broadcastTs: number): boolean {
  const seenTs: Record<string, number> = JSON.parse(localStorage.getItem(STORAGE_KEYS.NOTIFICATION_SEEN) || '{}')
  const seenRequests = readSeenRequests()
  const ids = seenRequests[ck]
  const known = seenTs[ck] !== undefined || ids !== undefined
  const fresh = ids === undefined || !ids.includes(requestId)
  if (fresh) {
    seenRequests[ck] = [...(ids ?? []), requestId].slice(-SEEN_REQUESTS_CAP)
    localStorage.setItem(STORAGE_KEYS.NOTIFICATION_SEEN_REQUESTS, JSON.stringify(seenRequests))
  }
  const stored = seenTs[ck]
  if (stored === undefined || broadcastTs > stored) {
    seenTs[ck] = broadcastTs
    localStorage.setItem(STORAGE_KEYS.NOTIFICATION_SEEN, JSON.stringify(seenTs))
  }
  return fresh && known
}

/** Steps one millisecond is split into for a request's Electron stamp (`requestBroadcastTs`). */
const REQUEST_STAMP_STEPS = 1024

/** FNV-1a, 32-bit. */
function hash32(s: string): number {
  let h = 0x811c9dc5
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i)
    h = Math.imul(h, 0x01000193)
  }
  return h >>> 0
}

/**
 * The `broadcastTs` handed to Electron for an event about one request. Electron main drops a number it showed in the
 * last 5 s (`recentBroadcasts` in electron/main.ts, one Set<number> for every window), so two requests stamped in the
 * same millisecond would collide on `since` alone and the second would never show. The request id moves the stamp
 * inside its millisecond by a fixed fraction:
 * - deterministic, so every window computes the same number for the same request and the multi-window dedup holds;
 * - strictly between `since` and `since + 1`, so it never equals an integer stamp another event carries;
 * - on a 1/1024 grid, which a double holds exactly for every stamp below 2^43 ms (year 2248) — a finer fraction would
 *   be rounded at today's epoch-ms magnitudes, possibly onto `since` or `since + 1`.
 * Two ids collide only when their hashes share a step (1 in 1023), on top of having to share the millisecond.
 */
export function requestBroadcastTs(since: number, requestId: string): number {
  return since + (1 + (hash32(requestId) % (REQUEST_STAMP_STEPS - 1))) / REQUEST_STAMP_STEPS
}

/** Remove a session's lastSeenTs entry and its seen request ids (called on SessionEnd to prevent
 *  stale timestamps from blocking notifications if the code is reused). */
export function clearSeenTs(sessionCode: string): void {
  const data: Record<string, number> = JSON.parse(localStorage.getItem(STORAGE_KEYS.NOTIFICATION_SEEN) || '{}')
  delete data[sessionCode]
  localStorage.setItem(STORAGE_KEYS.NOTIFICATION_SEEN, JSON.stringify(data))
  const requests = readSeenRequests()
  if (requests[sessionCode] !== undefined) {
    delete requests[sessionCode]
    localStorage.setItem(STORAGE_KEYS.NOTIFICATION_SEEN_REQUESTS, JSON.stringify(requests))
  }
}
