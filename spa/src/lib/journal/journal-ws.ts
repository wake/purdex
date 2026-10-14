// spa/src/lib/journal/journal-ws.ts — the daemon's `journal.changed` host event (daemon plan §8): `{unit, project, what}` after
// every drained write. Called from useMultiHostEventWs with the per-host closure's hostId, after its connection-key guard.
// The event ONLY marks the named resource of that unit stale (the store); it never causes a request — the journal loader
// re-reads the active unit, and a hidden unit is read when it becomes active. A malformed frame is dropped with one warning.
import { useJournalStore } from '../../stores/useJournalStore'
import { parseChangedEvent, warnOnce } from './parse'
import { unitKeyOf } from './types'

export const JOURNAL_EVENT_TYPES = ['journal.changed'] as const
export type JournalEventType = (typeof JOURNAL_EVENT_TYPES)[number]

export const isJournalEvent = (type: string): type is JournalEventType => (JOURNAL_EVENT_TYPES as readonly string[]).includes(type)

export function handleJournalEvent(hostId: string, _type: JournalEventType, value: unknown): void {
  const ev = parseChangedEvent(value)
  if (typeof ev === 'string') return warnOnce(`ignoring frame: ${ev}`)
  useJournalStore.getState().markStale(hostId, unitKeyOf({ kind: ev.unitKind, id: ev.unitId }), ev.what)
}
