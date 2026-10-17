// spa/src/lib/watch/watch-ws.ts — the daemon's two watcher host events (watcher area plan W5e). Called from useMultiHostEventWs
// with the per-host closure's hostId, after its connection-key check (a frame of a connection the host has left is dropped
// there). Neither frame is trusted for its content: `watch.changed` makes the host's status read again, `watch.events` makes its
// events read again, so the numbers shown are always the daemon's answers to a read and never a frame's own payload.
import { rereadWatchStatus } from './status-store'
import { rereadWatchEvents } from './events-store'

export const WATCH_CHANGED_EVENT = 'watch.changed'
export const WATCH_EVENTS_EVENT = 'watch.events'

export const isWatchEvent = (type: string): boolean => type === WATCH_CHANGED_EVENT || type === WATCH_EVENTS_EVENT

export function handleWatchEvent(hostId: string, type: string): void {
  if (type === WATCH_CHANGED_EVENT) rereadWatchStatus(hostId)
  else if (type === WATCH_EVENTS_EVENT) rereadWatchEvents(hostId)
}
