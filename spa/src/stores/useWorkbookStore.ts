// spa/src/stores/useWorkbookStore.ts — the session workbook, per host and conversation (WA-1; spec §9). Never persisted,
// never synced: every window has its own sockets and reloads what it shows.
//
// Loading rules (plan WA-1.3, codex #9) — the fetch count is bounded by seats × connection generations + views opened:
//   * `loadSeat`      — a seat's first appearance in a team view, and again once per connection generation (a new
//                       `/api/info` answer, support.gen): `limit: 1`, only on a host that lists `workbook.v1`.
//   * `openWorkbook`  — a workbook view opens: `limit: 20`; `loadMore` pages with `before = oldestId`.
//   * EVENTS NEVER FETCH. `workbook.entry` carries the entry (upsert by id, learns session → conversation, clears
//     `missing`); `workbook.status` carries `session_id`, so it lands after a reload too. A status for a conversation no
//     seat maps to yet is simply kept under its key.
// A fetch that lands after its host was forgotten or moved to another connection generation is dropped.
import { create } from 'zustand'
import { fetchConversation } from '../lib/workbook/api'
import { WORKBOOK_PROVIDER, type EntryEvent, type StatusEvent, type WorkbookEntry } from '../lib/workbook/types'

export const SEAT_PAGE = 1
export const VIEW_PAGE = 20

export interface ConvState {
  status: string
  statusAt: number
  /** Newest first, unique by id. */
  entries: WorkbookEntry[]
  /** The cursor for 「更多」 (`before=`): the oldest entry a page load brought in; null before any entry. */
  oldestId: number | null
  /** The last older page came back short: there is nothing before `oldestId`. */
  exhausted: boolean
  loading: boolean
  /** The daemon had no workbook for a session of this conversation in this connection generation (404). */
  missing: boolean
}

export interface WorkbookSupport { v1: boolean; v2: boolean }
interface HostSupport extends WorkbookSupport { gen: number }

interface WorkbookState {
  byHost: Record<string, { byConv: Record<string, ConvState> }>
  convOfSession: Record<string, Record<string, string>>
  support: Record<string, HostSupport>
  /** Sessions whose conversation answered 404 in the current generation. */
  missingSessions: Record<string, Record<string, true>>
  /** The generation each seat was last loaded in. */
  seatGen: Record<string, Record<string, number>>
  /** Bumped by `forgetHost`: tells a fetch still out that its host was forgotten. */
  epoch: Record<string, number>

  setSupport: (hostId: string, support: WorkbookSupport) => void
  loadSeat: (hostId: string, sessionId: string) => Promise<void>
  openWorkbook: (hostId: string, sessionId: string) => Promise<void>
  loadMore: (hostId: string, convKey: string) => Promise<void>
  applyEntry: (hostId: string, ev: EntryEvent) => void
  applyStatus: (hostId: string, ev: StatusEvent) => void
  forgetHost: (hostId: string) => void
  reset: () => void
}

const NO_SUPPORT: WorkbookSupport = { v1: false, v2: false }
const emptyConv = (): ConvState => ({ status: '', statusAt: 0, entries: [], oldestId: null, exhausted: false, loading: false, missing: false })

/** What a host's daemon supports, as of its last `/api/info` answer; both false until it answered. */
export function selectWorkbookSupport(hostId: string, state: Pick<WorkbookState, 'support'> = useWorkbookStore.getState()): WorkbookSupport {
  return state.support[hostId] ?? NO_SUPPORT // a stable reference (the generation rides along; callers read v1 / v2)
}

export const selectConv = (state: Pick<WorkbookState, 'byHost'>, hostId: string, convKey: string): ConvState | undefined =>
  state.byHost[hostId]?.byConv[convKey]

/** Merge `incoming` into a conversation's entries: newest first, one per id, a stale copy never replaces a newer one. */
function mergeEntries(have: WorkbookEntry[], incoming: WorkbookEntry[]): WorkbookEntry[] {
  const byId = new Map(have.map((e) => [e.id, e]))
  for (const e of incoming) {
    const cur = byId.get(e.id)
    if (!cur || e.updatedAt >= cur.updatedAt) byId.set(e.id, e)
  }
  return [...byId.values()].sort((a, b) => b.id - a.id)
}

type S = WorkbookState
function withConv(s: S, hostId: string, convKey: string, fn: (c: ConvState) => ConvState): Pick<S, 'byHost'> {
  const host = s.byHost[hostId] ?? { byConv: {} }
  return { byHost: { ...s.byHost, [hostId]: { byConv: { ...host.byConv, [convKey]: fn(host.byConv[convKey] ?? emptyConv()) } } } }
}
function withSessions(s: S, hostId: string, convKey: string, sessionIds: string[]): Pick<S, 'convOfSession' | 'missingSessions'> {
  const map = { ...s.convOfSession[hostId] }
  const missing = { ...s.missingSessions[hostId] }
  for (const id of sessionIds) { if (id) { map[id] = convKey; delete missing[id] } }
  return { convOfSession: { ...s.convOfSession, [hostId]: map }, missingSessions: { ...s.missingSessions, [hostId]: missing } }
}
const uniq = (xs: string[]): string[] => [...new Set(xs)]

export const useWorkbookStore = create<WorkbookState>()((set, get) => {
  /** One conversation fetch for `sessionId`, fenced to the host's epoch and generation. */
  async function runFetch(hostId: string, sessionId: string, q: { limit: number; before?: number }): Promise<void> {
    const epoch = get().epoch[hostId] ?? 0
    const gen = get().support[hostId]?.gen
    const known = get().convOfSession[hostId]?.[sessionId]
    if (known) set((s) => withConv(s, hostId, known, (c) => ({ ...c, loading: true })))
    const alive = () => (get().epoch[hostId] ?? 0) === epoch && get().support[hostId]?.gen === gen
    let result
    try {
      result = await fetchConversation(hostId, WORKBOOK_PROVIDER, sessionId, q)
    } catch {
      if (alive() && known) set((s) => withConv(s, hostId, known, (c) => ({ ...c, loading: false })))
      return
    }
    if (!alive()) return // another daemon, or another connection: the old answer is not this one's
    if (result.kind === 'not_found') {
      set((s) => ({
        missingSessions: { ...s.missingSessions, [hostId]: { ...s.missingSessions[hostId], [sessionId]: true } },
        ...(known ? withConv(s, hostId, known, (c) => ({ ...c, loading: false, missing: true })) : {}),
      }))
      return
    }
    const { page } = result
    set((s) => ({
      ...withSessions(s, hostId, page.convKey, uniq([sessionId, ...page.entries.map((e) => e.sessionId)])),
      ...withConv(s, hostId, page.convKey, (c) => {
        const ids = page.entries.map((e) => e.id)
        const pageOldest = ids.length ? Math.min(...ids) : null
        const older = q.before !== undefined
        const fresh = page.statusAt >= c.statusAt
        return {
          ...c,
          status: fresh ? page.status : c.status,
          statusAt: fresh ? page.statusAt : c.statusAt,
          entries: mergeEntries(c.entries, page.entries),
          oldestId: older ? (pageOldest ?? c.oldestId) : (c.oldestId ?? pageOldest),
          exhausted: older || c.oldestId === null ? page.entries.length < q.limit : c.exhausted,
          loading: false, missing: false,
        }
      }),
    }))
  }

  return {
    byHost: {}, convOfSession: {}, support: {}, missingSessions: {}, seatGen: {}, epoch: {},

    setSupport: (hostId, support) => set((s) => {
      // A new answer is a new connection generation: what 404'd or was loading on the last one is asked again.
      const byConv = s.byHost[hostId]?.byConv ?? {}
      const cleared = Object.fromEntries(Object.entries(byConv).map(([k, c]) => [k, { ...c, loading: false, missing: false }]))
      return {
        support: { ...s.support, [hostId]: { ...support, gen: (s.support[hostId]?.gen ?? 0) + 1 } },
        missingSessions: { ...s.missingSessions, [hostId]: {} },
        ...(s.byHost[hostId] ? { byHost: { ...s.byHost, [hostId]: { byConv: cleared } } } : {}),
      }
    }),

    loadSeat: async (hostId, sessionId) => {
      const sup = get().support[hostId]
      if (!sup?.v1) return // not a workbook host (or not known to be one yet)
      if (get().seatGen[hostId]?.[sessionId] === sup.gen) return // once per seat per connection generation
      set((s) => ({ seatGen: { ...s.seatGen, [hostId]: { ...s.seatGen[hostId], [sessionId]: sup.gen } } }))
      await runFetch(hostId, sessionId, { limit: SEAT_PAGE })
    },

    openWorkbook: async (hostId, sessionId) => {
      if (!get().support[hostId]?.v1) return
      const conv = get().convOfSession[hostId]?.[sessionId]
      if (conv && get().byHost[hostId]?.byConv[conv]?.loading) return
      await runFetch(hostId, sessionId, { limit: VIEW_PAGE })
    },

    loadMore: async (hostId, convKey) => {
      const c = get().byHost[hostId]?.byConv[convKey]
      if (!get().support[hostId]?.v1 || !c || c.loading || c.exhausted || c.oldestId === null) return
      await runFetch(hostId, convKey, { limit: VIEW_PAGE, before: c.oldestId }) // a conversation key is a session of it
    },

    applyEntry: (hostId, ev) => set((s) => ({
      ...withSessions(s, hostId, ev.convKey, uniq([ev.sessionId, ev.entry.sessionId])),
      ...withConv(s, hostId, ev.convKey, (c) => ({
        ...c,
        entries: mergeEntries(c.entries, [ev.entry]),
        oldestId: c.oldestId ?? ev.entry.id, // pages walk back from the first entry this window knew
        missing: false,
      })),
    })),

    applyStatus: (hostId, ev) => set((s) => ({
      ...withSessions(s, hostId, ev.convKey, [ev.sessionId]),
      ...withConv(s, hostId, ev.convKey, (c) => (ev.updatedAt >= c.statusAt ? { ...c, status: ev.status, statusAt: ev.updatedAt, missing: false } : c)),
    })),

    forgetHost: (hostId) => set((s) => {
      const drop = <T>(m: Record<string, T>): Record<string, T> => { const { [hostId]: _g, ...rest } = m; return rest }
      return {
        byHost: drop(s.byHost), convOfSession: drop(s.convOfSession), support: drop(s.support),
        missingSessions: drop(s.missingSessions), seatGen: drop(s.seatGen), epoch: { ...s.epoch, [hostId]: (s.epoch[hostId] ?? 0) + 1 },
      }
    }),

    reset: () => set({ byHost: {}, convOfSession: {}, support: {}, missingSessions: {}, seatGen: {}, epoch: {} }),
  }
})
