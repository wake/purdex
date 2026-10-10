// spa/src/stores/useWorkbookStore.ts — the session workbook, per host and conversation (WA-1; spec §9). Never persisted,
// never synced: every window has its own sockets and reloads what it shows.
//
// Loading rules (plan WA-1.3, codex #9) — the fetch count is bounded by seats × connection generations + views opened:
//   * `loadSeat`      — a seat's first appearance in a team view, and again once per connection generation (a new
//                       connection generation, `gens`): `limit: 1`, only on a host that lists `workbook.v1`.
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

// Retention (events can name any conversation of the host, so the store must not grow with them). Per host: at most
// MAX_CONVS conversations, of which at most MAX_UNPINNED are ones nobody holds (no seat maps to them and no view is
// open on them), least recently touched out first; per conversation the MAX_ENTRIES newest entries.
export const MAX_CONVS = 200
export const MAX_UNPINNED = 50
export const MAX_ENTRIES = 500

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
  /** Recency stamp for eviction (a store-wide counter, bumped by every write to the conversation). */
  touched: number
  /** The daemon had no workbook for a session of this conversation in this connection generation (404). */
  missing: boolean
}

export interface WorkbookSupport { v1: boolean; v2: boolean }

interface WorkbookState {
  byHost: Record<string, { byConv: Record<string, ConvState> }>
  convOfSession: Record<string, Record<string, string>>
  support: Record<string, WorkbookSupport>
  /** The connection generation per host: `fence` bumps it when the connection is interrupted or a new probe starts. */
  gens: Record<string, number>
  /** Sessions whose conversation answered 404 in the current generation. */
  missingSessions: Record<string, Record<string, true>>
  /** The generation each seat was last loaded in. */
  seatGen: Record<string, Record<string, number>>
  /** Conversations a view is open on (reference counts): never evicted. */
  viewing: Record<string, Record<string, number>>
  /** Bumped by `forgetHost`: tells a fetch still out that its host was forgotten. */
  epoch: Record<string, number>

  /** The connection the host had is over, or a new probe starts: support is unknown again and no answer still out may land. */
  fence: (hostId: string) => void
  /** The probe's answer for the current generation (it does not start a new one). */
  setSupport: (hostId: string, support: WorkbookSupport) => void
  loadSeat: (hostId: string, sessionId: string) => Promise<void>
  openWorkbook: (hostId: string, sessionId: string) => Promise<void>
  /** A workbook view opened (true) or closed (false) on a conversation: it is not evicted while open. */
  setViewing: (hostId: string, convKey: string, open: boolean) => void
  loadMore: (hostId: string, convKey: string) => Promise<void>
  applyEntry: (hostId: string, ev: EntryEvent) => void
  applyStatus: (hostId: string, ev: StatusEvent) => void
  forgetHost: (hostId: string) => void
  reset: () => void
}

const NO_SUPPORT: WorkbookSupport = { v1: false, v2: false }
const emptyConv = (): ConvState => ({ status: '', statusAt: 0, entries: [], oldestId: null, exhausted: false, loading: false, touched: 0, missing: false })

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
let clock = 0
/** The newest MAX_ENTRIES entries; the paging cursor never points at a dropped one, and older entries exist again. */
function capped(c: ConvState): ConvState {
  if (c.entries.length <= MAX_ENTRIES) return c
  const entries = c.entries.slice(0, MAX_ENTRIES)
  return { ...c, entries, oldestId: entries[entries.length - 1].id, exhausted: false }
}
function withConv(s: S, hostId: string, convKey: string, fn: (c: ConvState) => ConvState): Pick<S, 'byHost'> {
  const host = s.byHost[hostId] ?? { byConv: {} }
  const next = capped({ ...fn(host.byConv[convKey] ?? emptyConv()), touched: ++clock })
  return { byHost: { ...s.byHost, [hostId]: { byConv: { ...host.byConv, [convKey]: next } } } }
}
/** Drop the least recently touched conversations nobody holds until the host is within its bounds. */
function evicted(s: S, hostId: string): Partial<S> {
  const byConv = s.byHost[hostId]?.byConv
  if (!byConv) return {}
  const held = new Set<string>()
  for (const sid of Object.keys(s.seatGen[hostId] ?? {})) { const k = s.convOfSession[hostId]?.[sid]; if (k) held.add(k) }
  for (const k of Object.keys(s.viewing[hostId] ?? {})) held.add(k)
  const keys = Object.keys(byConv)
  const loose = keys.filter((k) => !held.has(k)).sort((a, b) => byConv[a].touched - byConv[b].touched)
  const drop = Math.max(loose.length - MAX_UNPINNED, keys.length - MAX_CONVS, 0)
  if (drop === 0) return {}
  const gone = new Set(loose.slice(0, drop))
  const nextConv = Object.fromEntries(Object.entries(byConv).filter(([k]) => !gone.has(k)))
  const map = Object.fromEntries(Object.entries(s.convOfSession[hostId] ?? {}).filter(([, k]) => !gone.has(k)))
  const miss = Object.fromEntries(Object.entries(s.missingSessions[hostId] ?? {}).filter(([sid]) => !(sid in (s.convOfSession[hostId] ?? {})) || sid in map))
  return {
    byHost: { ...s.byHost, [hostId]: { byConv: nextConv } },
    convOfSession: { ...s.convOfSession, [hostId]: map },
    missingSessions: { ...s.missingSessions, [hostId]: miss as Record<string, true> },
  }
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
    const gen = get().gens[hostId]
    const known = get().convOfSession[hostId]?.[sessionId]
    if (known) set((s) => withConv(s, hostId, known, (c) => ({ ...c, loading: true })))
    const alive = () => (get().epoch[hostId] ?? 0) === epoch && get().gens[hostId] === gen
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
    set((s) => evicted(s, hostId))
  }

  return {
    byHost: {}, convOfSession: {}, support: {}, gens: {}, missingSessions: {}, seatGen: {}, viewing: {}, epoch: {},

    fence: (hostId) => set((s) => {
      // What 404'd or was loading on the last connection is asked again on the next; entries already loaded stay (they
      // are history), but support is unknown until the new answer, so nothing is fetched and no view may rely on v1.
      const byConv = s.byHost[hostId]?.byConv ?? {}
      const cleared = Object.fromEntries(Object.entries(byConv).map(([k, c]) => [k, { ...c, loading: false, missing: false }]))
      const { [hostId]: _gone, ...support } = s.support
      return {
        support, gens: { ...s.gens, [hostId]: (s.gens[hostId] ?? 0) + 1 },
        missingSessions: { ...s.missingSessions, [hostId]: {} },
        ...(s.byHost[hostId] ? { byHost: { ...s.byHost, [hostId]: { byConv: cleared } } } : {}),
      }
    }),

    setSupport: (hostId, support) => set((s) => {
      const cur = s.support[hostId]
      return cur && cur.v1 === support.v1 && cur.v2 === support.v2 ? s : { support: { ...s.support, [hostId]: { v1: support.v1, v2: support.v2 } } }
    }),

    loadSeat: async (hostId, sessionId) => {
      const sup = get().support[hostId]
      if (!sup?.v1) return // not a workbook host (or not known to be one yet)
      const gen = get().gens[hostId] ?? 0
      if (get().seatGen[hostId]?.[sessionId] === gen) return // once per seat per connection generation
      set((s) => ({ seatGen: { ...s.seatGen, [hostId]: { ...s.seatGen[hostId], [sessionId]: gen } } }))
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

    setViewing: (hostId, convKey, open) => set((s) => {
      const cur = { ...s.viewing[hostId] }
      const n = (cur[convKey] ?? 0) + (open ? 1 : -1)
      if (n > 0) cur[convKey] = n
      else delete cur[convKey]
      return { viewing: { ...s.viewing, [hostId]: cur } }
    }),

    applyEntry: (hostId, ev) => { set((s) => ({
      ...withSessions(s, hostId, ev.convKey, uniq([ev.sessionId, ev.entry.sessionId])),
      ...withConv(s, hostId, ev.convKey, (c) => ({
        ...c,
        entries: mergeEntries(c.entries, [ev.entry]),
        oldestId: c.oldestId ?? ev.entry.id, // pages walk back from the first entry this window knew
        missing: false,
      })),
    })); set((s) => evicted(s, hostId)) },

    applyStatus: (hostId, ev) => { set((s) => ({
      ...withSessions(s, hostId, ev.convKey, [ev.sessionId]),
      ...withConv(s, hostId, ev.convKey, (c) => (ev.updatedAt >= c.statusAt ? { ...c, status: ev.status, statusAt: ev.updatedAt, missing: false } : c)),
    })); set((s) => evicted(s, hostId)) },

    forgetHost: (hostId) => set((s) => {
      const drop = <T>(m: Record<string, T>): Record<string, T> => { const { [hostId]: _g, ...rest } = m; return rest }
      return {
        byHost: drop(s.byHost), convOfSession: drop(s.convOfSession), support: drop(s.support), gens: drop(s.gens),
        missingSessions: drop(s.missingSessions), seatGen: drop(s.seatGen), viewing: drop(s.viewing), epoch: { ...s.epoch, [hostId]: (s.epoch[hostId] ?? 0) + 1 },
      }
    }),

    reset: () => { set({ byHost: {}, convOfSession: {}, support: {}, gens: {}, missingSessions: {}, seatGen: {}, viewing: {}, epoch: {} }) },
  }
})
