// spa/src/lib/team/__tests__/workbook-fixture.ts — a seat's workbook seeded straight into the real workbook store (WA-2b-1 tests).
import { useWorkbookStore, type ConvState } from '../../../stores/useWorkbookStore'
import { emptyTodos } from '../../workbook/merge'
import type { WorkbookEntry } from '../../workbook/types'

export const conv = (over: Partial<ConvState> = {}): ConvState => ({
  status: '', statusAt: 1, entries: [], oldestId: null, exhausted: false, loading: false, touched: 1, missing: false,
  todos: emptyTodos(), refreshAvailable: false, availAt: 0, appliedAt: 0, ...over,
})

export const entry = (id: number, over: Partial<WorkbookEntry> = {}): WorkbookEntry => ({
  id, convKey: 'c-S', sessionId: 'S', turnId: `t${id}`, turnAt: id, state: 'ok', reason: '', thing: `thing ${id}`, push: '',
  entry: `entry ${id}`, thingDone: false, createdAt: id, updatedAt: id, kind: 'turn',
  usage: { in: 0, out: 0, cacheRead: 0 },
  todoChanges: { added: [], done: [], dropped: [] },
  ...over,
})

/** Seed `sessionId` of `hostId` with a conversation `c-<sessionId>`; the host lists workbook.v1 unless `support` says otherwise. */
export function seedWorkbook(hostId: string, sessionId: string, over: Partial<ConvState> = {}, support: { v1?: boolean; v2?: boolean } = {}): string {
  const convKey = `c-${sessionId}`
  useWorkbookStore.setState((s) => ({
    support: { ...s.support, [hostId]: { v1: support.v1 ?? true, v2: support.v2 ?? false } },
    convOfSession: { ...s.convOfSession, [hostId]: { ...s.convOfSession[hostId], [sessionId]: convKey } },
    byHost: { ...s.byHost, [hostId]: { byConv: { ...s.byHost[hostId]?.byConv, [convKey]: conv(over) } } },
  }))
  return convKey
}
