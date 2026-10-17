// spa/src/lib/watch/watch-ws.ts — the daemon's two watcher host events (watcher area plan W5e). Called from useMultiHostEventWs
// with the per-host closure's hostId, after its connection-key check (a frame of a connection the host has left is dropped
// there).
//   - `watch.changed` makes the host's status read again (its payload is not used for anything shown).
//   - `watch.events` is `{host_id, unhandled, cause}`: `unhandled` is the daemon's own count and becomes that host's count at once;
//     the host's event list is read again as well. A payload that cannot be read leaves the count as it was.
import { rereadWatchStatus } from './status-store'
import { applyWatchUnhandled, rereadWatchEvents } from './events-store'

export const WATCH_CHANGED_EVENT = 'watch.changed'
export const WATCH_EVENTS_EVENT = 'watch.events'

export const isWatchEvent = (type: string): boolean => type === WATCH_CHANGED_EVENT || type === WATCH_EVENTS_EVENT

/** The `unhandled` of a `watch.events` value (a JSON string, or already parsed), or undefined. */
function unhandledOf(value: unknown): unknown {
  let body = value
  if (typeof value === 'string') {
    try { body = JSON.parse(value) } catch { return undefined }
  }
  return typeof body === 'object' && body !== null ? (body as { unhandled?: unknown }).unhandled : undefined
}

export function handleWatchEvent(hostId: string, type: string, value?: unknown): void {
  if (type === WATCH_CHANGED_EVENT) {
    rereadWatchStatus(hostId)
  } else if (type === WATCH_EVENTS_EVENT) {
    const n = unhandledOf(value)
    if (typeof n === 'number') applyWatchUnhandled(hostId, n)
    rereadWatchEvents(hostId)
  }
}
