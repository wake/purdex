// spa/src/lib/workbook/workbook-ws.ts — the daemon's workbook host events (spec §9): `workbook.entry` (carries the whole
// entry) and `workbook.status` (carries `session_id`). Called from useMultiHostEventWs with the per-host closure's
// hostId, after its connection-key guard. A malformed frame is dropped (one warning per kind of malformation).
// An event only writes the store: it never causes a request (plan WA-1.3).
import { useWorkbookStore } from '../../stores/useWorkbookStore'
import { parseEntryEvent, parseStatusEvent, warnOnce } from './parse'

export const WORKBOOK_EVENT_TYPES = ['workbook.entry', 'workbook.status'] as const
export type WorkbookEventType = (typeof WORKBOOK_EVENT_TYPES)[number]

export const isWorkbookEvent = (type: string): type is WorkbookEventType => (WORKBOOK_EVENT_TYPES as readonly string[]).includes(type)

export function handleWorkbookEvent(hostId: string, type: WorkbookEventType, value: unknown): void {
  const store = useWorkbookStore.getState()
  if (type === 'workbook.entry') {
    const ev = parseEntryEvent(value)
    if (typeof ev === 'string') return warnOnce(`ignoring frame: ${ev}`)
    store.applyEntry(hostId, ev)
  } else {
    const ev = parseStatusEvent(value)
    if (typeof ev === 'string') return warnOnce(`ignoring frame: ${ev}`)
    store.applyStatus(hostId, ev)
  }
}
