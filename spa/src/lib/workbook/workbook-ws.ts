// spa/src/lib/workbook/workbook-ws.ts — the daemon's workbook host events (spec §9): `workbook.entry` (carries the whole
// entry), `workbook.status` (carries `session_id`) and, from a `workbook.v2` daemon, `workbook.todos` (the todos that
// changed) and `workbook.refresh_available`. Called from useMultiHostEventWs with the per-host closure's hostId, after its
// connection-key guard. A malformed frame is dropped (one warning per kind of malformation).
// An event only writes the store: it never causes a request (plan WA-1.3).
import { useWorkbookStore } from '../../stores/useWorkbookStore'
import { parseEntryEvent, parseRefreshAvailableEvent, parseStatusEvent, parseTodosEvent, warnOnce } from './parse'

export const WORKBOOK_EVENT_TYPES = ['workbook.entry', 'workbook.status', 'workbook.todos', 'workbook.refresh_available'] as const
export type WorkbookEventType = (typeof WORKBOOK_EVENT_TYPES)[number]

export const isWorkbookEvent = (type: string): type is WorkbookEventType => (WORKBOOK_EVENT_TYPES as readonly string[]).includes(type)

export function handleWorkbookEvent(hostId: string, type: WorkbookEventType, value: unknown): void {
  const store = useWorkbookStore.getState()
  switch (type) {
    case 'workbook.entry': {
      const ev = parseEntryEvent(value)
      if (typeof ev === 'string') return warnOnce(`ignoring frame: ${ev}`)
      return store.applyEntry(hostId, ev)
    }
    case 'workbook.status': {
      const ev = parseStatusEvent(value)
      if (typeof ev === 'string') return warnOnce(`ignoring frame: ${ev}`)
      return store.applyStatus(hostId, ev)
    }
    case 'workbook.todos': {
      const ev = parseTodosEvent(value)
      if (typeof ev === 'string') return warnOnce(`ignoring frame: ${ev}`)
      return store.applyTodos(hostId, ev)
    }
    case 'workbook.refresh_available': {
      const ev = parseRefreshAvailableEvent(value)
      if (typeof ev === 'string') return warnOnce(`ignoring frame: ${ev}`)
      return store.applyRefreshAvailable(hostId, ev)
    }
  }
}
