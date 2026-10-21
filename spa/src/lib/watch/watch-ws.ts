// spa/src/lib/watch/watch-ws.ts — the daemon's watcher host event (watcher area plan W5e). Called from useMultiHostEventWs
// with the per-host closure's hostId, after its connection-key check (a frame of a connection the host has left is dropped
// there).
//   - `watch.changed` makes the host's status read again (its payload is not used for anything shown).
import { rereadWatchStatus } from './status-store'

export const WATCH_CHANGED_EVENT = 'watch.changed'

export const isWatchEvent = (type: string): boolean => type === WATCH_CHANGED_EVENT

export function handleWatchEvent(hostId: string, type: string, _value?: unknown): void {
  if (type === WATCH_CHANGED_EVENT) rereadWatchStatus(hostId)
}
