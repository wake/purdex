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
//
// v2 (plan WA-1.6 / 1.7; needs `workbook.v2`, else nothing below is fetched or posted):
//   * `todos`          — open (oldest first) and done (newest first, `loadMoreDone` pages it). The conversation answer is a
//                        snapshot, `workbook.todos` events upsert by id; a todo only moves forward (open → done / dropped), so a
//                        stale copy never revives a closed one. EVENTS STILL NEVER FETCH.
//   * `refreshAvailable` — the conversation answer's flag, then `workbook.refresh_available`.
//   * refresh pending  — DERIVED (`selectRefreshPending`): the conversation holds a `kind: refresh` entry that is `pending`.
//                        Nothing is stored, so there is no flag to get stuck: the entry's terminal event or any refetch that
//                        sees it terminal ends it, a reconnect included. `requestRefresh` upserts the 202's entry as pending.
//   * `loadUntil`      — pages `before=` from the oldest loaded entry until an entry is loaded, at most MAX_UNTIL_PAGES pages.
import { create } from 'zustand'
import { fetchConversation, fetchTodos, postRefresh } from '../lib/workbook/api'
import {
  WORKBOOK_PROVIDER, type EntryEvent, type RefreshAvailableEvent, type StatusEvent, type TodoLists, type TodosEvent, type WorkbookEntry, type WorkbookTodo,
} from '../lib/workbook/types'

export const SEAT_PAGE = 1
export const VIEW_PAGE = 20
/** `loadUntil` gives up after this many pages (5 × VIEW_PAGE = 100 entries). */
export const MAX_UNTIL_PAGES = 5

// Retention (events can name any conversation of the host, so the store must not grow with them). Per host: at most
// MAX_CONVS conversations, of which at most MAX_UNPINNED are ones nobody holds (no seat maps to them and no view is
// open on them), least recently touched out first; per conversation the MAX_ENTRIES newest entries, the MAX_OPEN_TODOS
// newest open todos (the daemon keeps 30 open at most), the MAX_DONE_TODOS newest done ones and the ids of the last
// MAX_DROPPED_TOMBSTONES dropped ones (so a stale open copy cannot revive them).
export const MAX_CONVS = 200
export const MAX_UNPINNED = 50
export const MAX_ENTRIES = 500
export const MAX_OPEN_TODOS = 50
export const MAX_DONE_TODOS = 200
export const MAX_DROPPED_TOMBSTONES = 100

export interface TodoBook {
  /** Oldest first. */
  open: WorkbookTodo[]
  /** Newest first. */
  done: WorkbookTodo[]
  /** The cursor for paging the done record (`before=`): the oldest done todo loaded; null while none is. */
  doneOldestId: number | null
  /** The last older done page came back short: there is nothing before `doneOldestId`. */
  doneExhausted: boolean
  loading: boolean
  /** Dropped todo ids, newest last (bounded). */
  droppedIds: number[]
}

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
  /** v2: the todo list (empty on a v1 daemon). */
  todos: TodoBook
  /** v2: some live session of the conversation can run a refresh now (false until told, and again after a disconnect). */
  refreshAvailable: boolean
  /** Store clock stamp of the last `workbook.refresh_available` event: an answer whose request started before it is older than the event. */
  availAt: number
}

/** What `requestRefresh` answers; it never throws. `unsupported`: the host does not list `workbook.v2` (nothing was posted). */
export type RefreshOutcome =
  | { kind: 'accepted'; entryId: number }
  | { kind: 'not_live' }
  | { kind: 'refresh_pending' }
  | { kind: 'unsupported' }
  | { kind: 'error'; code: string }

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
  /** The seats the team views show now: a seat that is gone stops holding its conversation (and counts as new if it returns). */
  syncSeats: (targets: ReadonlyArray<{ hostId: string; sessionId: string }>) => void
  /** A workbook view opened (true) or closed (false) on a conversation: it is not evicted while open. */
  setViewing: (hostId: string, convKey: string, open: boolean) => void
  loadMore: (hostId: string, convKey: string) => Promise<void>
  /** v2: the next older page of the done record (`before = doneOldestId`). */
  loadMoreDone: (hostId: string, convKey: string) => Promise<void>
  /** v2: ask for a refresh; a 202 upserts its entry as pending (which is what makes refresh pending true). */
  requestRefresh: (hostId: string, convKey: string) => Promise<RefreshOutcome>
  /** Page `before=` from the oldest loaded entry until `entryId` is loaded: true when it is, false after MAX_UNTIL_PAGES pages or when it is not there. */
  loadUntil: (hostId: string, convKey: string, entryId: number) => Promise<boolean>
  applyEntry: (hostId: string, ev: EntryEvent) => void
  applyStatus: (hostId: string, ev: StatusEvent) => void
  applyTodos: (hostId: string, ev: TodosEvent) => void
  applyRefreshAvailable: (hostId: string, ev: RefreshAvailableEvent) => void
  forgetHost: (hostId: string) => void
  reset: () => void
}

const NO_SUPPORT: WorkbookSupport = { v1: false, v2: false }
const emptyTodos = (): TodoBook => ({ open: [], done: [], doneOldestId: null, doneExhausted: false, loading: false, droppedIds: [] })
const emptyConv = (): ConvState => ({
  status: '', statusAt: 0, entries: [], oldestId: null, exhausted: false, loading: false, touched: 0, missing: false,
  todos: emptyTodos(), refreshAvailable: false, availAt: 0,
})

/** Refresh pending, derived: the conversation holds a refresh entry that is still pending (see the header). */
export const selectRefreshPending = (c: Pick<ConvState, 'entries'> | undefined): boolean =>
  !!c && c.entries.some((e) => e.kind === 'refresh' && e.state === 'pending')

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

/** Fold `incoming` todos into the book by id. A todo only moves forward: an open copy never replaces a closed one, a
 *  dropped one never comes back, a done one is never dropped. Bounded (open: newest kept; done: newest kept, the cursor
 *  follows and older done records exist again). */
function upsertTodos(t: TodoBook, incoming: WorkbookTodo[]): TodoBook {
  const open = new Map(t.open.map((x) => [x.id, x]))
  const done = new Map(t.done.map((x) => [x.id, x]))
  const dropped = new Set(t.droppedIds)
  for (const x of incoming) {
    if (done.has(x.id) && x.state !== 'done') continue
    if (dropped.has(x.id)) continue
    if (x.state === 'open') open.set(x.id, x)
    else if (x.state === 'done') { open.delete(x.id); done.set(x.id, x) }
    else { open.delete(x.id); dropped.add(x.id) }
  }
  let openList = [...open.values()].sort((a, b) => a.id - b.id)
  if (openList.length > MAX_OPEN_TODOS) openList = openList.slice(openList.length - MAX_OPEN_TODOS)
  let doneList = [...done.values()].sort((a, b) => b.id - a.id)
  let doneExhausted = t.doneExhausted
  if (doneList.length > MAX_DONE_TODOS) { doneList = doneList.slice(0, MAX_DONE_TODOS); doneExhausted = false }
  return {
    ...t, open: openList, done: doneList, doneExhausted, droppedIds: [...dropped].slice(-MAX_DROPPED_TOMBSTONES),
    doneOldestId: doneList.length ? doneList[doneList.length - 1].id : null,
  }
}

/** A conversation answer's todos are a snapshot as of its response: open is replaced by it — except todos newer than
 *  anything in it (an event that landed while the request was out). */
function snapshotTodos(t: TodoBook, snap: TodoLists): TodoBook {
  const newest = Math.max(0, ...snap.open.map((x) => x.id), ...snap.done.map((x) => x.id))
  return upsertTodos(upsertTodos({ ...t, open: t.open.filter((x) => x.id > newest) }, snap.done), snap.open)
}

type S = WorkbookState
let clock = 0
/** The newest MAX_ENTRIES entries; the paging cursor never points at a dropped one, and older entries exist again. */
function capped(c: ConvState): ConvState {
  if (c.entries.length <= MAX_ENTRIES) return c
  const entries = c.entries.slice(0, MAX_ENTRIES)
  return { ...c, entries, oldestId: entries[entries.length - 1].id, exhausted: false }
}
/** Any write that has todos in hand goes through `upsertTodos`/`snapshotTodos`, which bound them. */
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
  const inflight = new Map<string, Promise<void>>()
  /** One request per (host, session, page) and connection generation at a time: a repeat joins the one out, whether or not the
   *  session's conversation is known yet (a non-team session has no mapping before its first answer). */
  function runFetch(hostId: string, sessionId: string, q: { limit: number; before?: number }): Promise<void> {
    const key = JSON.stringify([hostId, get().epoch[hostId] ?? 0, get().gens[hostId] ?? 0, sessionId, q.limit, q.before ?? null])
    const out = inflight.get(key)
    if (out) return out
    const p = fetchOnce(hostId, sessionId, q).finally(() => { if (inflight.get(key) === p) inflight.delete(key) })
    inflight.set(key, p)
    return p
  }
  async function fetchOnce(hostId: string, sessionId: string, q: { limit: number; before?: number }): Promise<void> {
    const epoch = get().epoch[hostId] ?? 0
    const gen = get().gens[hostId]
    const startedAt = ++clock // stamps the request: an event that lands after it is newer than the answer
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
          // v2 parts (null from a v1 daemon: left as they were)
          todos: page.todos ? snapshotTodos(c.todos, page.todos) : c.todos,
          refreshAvailable: page.refreshAvailable !== null && c.availAt < startedAt ? page.refreshAvailable : c.refreshAvailable,
        }
      }),
    }))
    set((s) => evicted(s, hostId))
  }

  /** A session of the conversation to name in a request: a known one (the daemon wants a real session for a refresh), else the key (any session of it resolves). */
  function sessionOf(hostId: string, convKey: string): string {
    const map = get().convOfSession[hostId] ?? {}
    return Object.keys(map).find((sid) => map[sid] === convKey) ?? convKey
  }

  return {
    byHost: {}, convOfSession: {}, support: {}, gens: {}, missingSessions: {}, seatGen: {}, viewing: {}, epoch: {},

    fence: (hostId) => set((s) => {
      // What 404'd or was loading on the last connection is asked again on the next; entries already loaded stay (they
      // are history), but support is unknown until the new answer, so nothing is fetched and no view may rely on v1.
      const byConv = s.byHost[hostId]?.byConv ?? {}
      // `refreshAvailable` is live state of the old connection: false until the new one says. Refresh pending needs no reset (derived).
      const cleared = Object.fromEntries(Object.entries(byConv).map(([k, c]) => [k, { ...c, loading: false, missing: false, refreshAvailable: false, todos: { ...c.todos, loading: false } }]))
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

    loadMoreDone: async (hostId, convKey) => {
      const c = get().byHost[hostId]?.byConv[convKey]
      if (!get().support[hostId]?.v2 || !c || c.todos.loading || c.todos.doneExhausted) return
      const epoch = get().epoch[hostId] ?? 0
      const gen = get().gens[hostId]
      const alive = () => (get().epoch[hostId] ?? 0) === epoch && get().gens[hostId] === gen
      const before = c.todos.doneOldestId ?? undefined
      const setLoading = (loading: boolean) => set((s) => withConv(s, hostId, convKey, (x) => ({ ...x, todos: { ...x.todos, loading } })))
      setLoading(true)
      let r
      try {
        r = await fetchTodos(hostId, sessionOf(hostId, convKey), { state: 'done', limit: VIEW_PAGE, before })
      } catch {
        if (alive()) setLoading(false)
        return
      }
      if (!alive()) return
      if (r.kind === 'not_found') { setLoading(false); return }
      const { todos } = r
      set((s) => withConv(s, hostId, convKey, (x) => ({
        ...x, todos: { ...upsertTodos(x.todos, todos), loading: false, doneExhausted: x.todos.doneExhausted || todos.length < VIEW_PAGE },
      })))
    },

    requestRefresh: async (hostId, convKey) => {
      if (!get().support[hostId]?.v2) return { kind: 'unsupported' }
      const epoch = get().epoch[hostId] ?? 0
      const gen = get().gens[hostId]
      const sessionId = sessionOf(hostId, convKey)
      let r
      try {
        r = await postRefresh(hostId, sessionId)
      } catch (e) {
        const code = (e as { code?: unknown } | null)?.code
        return { kind: 'error', code: typeof code === 'string' ? code : 'network' }
      }
      if (r.kind !== 'accepted') return r
      if ((get().epoch[hostId] ?? 0) === epoch && get().gens[hostId] === gen) {
        // The 202's entry is pending until its terminal event (or a refetch) says otherwise: that IS "refresh pending".
        // updatedAt 0, so any real copy of the entry (an event that outran this answer included) wins over this stand-in.
        const stub: WorkbookEntry = {
          id: r.entryId, convKey, sessionId, turnId: '', turnAt: 0, state: 'pending', reason: '', thing: '', push: '', entry: '', thingDone: false,
          createdAt: Date.now(), updatedAt: 0, kind: 'refresh', usage: { in: 0, out: 0, cacheRead: 0 }, todoChanges: { added: [], done: [], dropped: [] },
        }
        set((s) => withConv(s, hostId, convKey, (c) => ({ ...c, entries: mergeEntries(c.entries, [stub]), oldestId: c.oldestId ?? stub.id })))
        set((s) => evicted(s, hostId))
      }
      return r
    },

    loadUntil: async (hostId, convKey, entryId) => {
      if (!get().support[hostId]?.v1) return false
      const loaded = () => get().byHost[hostId]?.byConv[convKey]?.entries.some((e) => e.id === entryId) ?? false
      for (let page = 0; page < MAX_UNTIL_PAGES; page++) {
        if (loaded()) return true
        const c = get().byHost[hostId]?.byConv[convKey]
        // Ids only grow: past the id (or out of older entries) and still not loaded means it is not there.
        if (c && (c.exhausted || (c.oldestId !== null && c.oldestId < entryId))) return false
        const before = c?.oldestId ?? undefined
        await runFetch(hostId, sessionOf(hostId, convKey), before === undefined ? { limit: VIEW_PAGE } : { limit: VIEW_PAGE, before })
        const after = get().byHost[hostId]?.byConv[convKey]?.oldestId ?? null
        if (after === null || after === before) return loaded() // the page did not move the cursor (failed, fenced, empty): stop
      }
      return loaded()
    },

    syncSeats: (targets) => set((s) => {
      const now = new Set(targets.map((t) => `${t.hostId}\u0000${t.sessionId}`))
      let next: S = s
      for (const [hostId, seats] of Object.entries(s.seatGen)) {
        const kept = Object.fromEntries(Object.entries(seats).filter(([sid]) => now.has(`${hostId}\u0000${sid}`)))
        if (Object.keys(kept).length === Object.keys(seats).length) continue
        next = { ...next, seatGen: { ...next.seatGen, [hostId]: kept } }
        next = { ...next, ...evicted(next, hostId) }
      }
      return next === s ? s : next
    }),

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

    applyTodos: (hostId, ev) => { set((s) => ({
      ...withSessions(s, hostId, ev.convKey, [ev.sessionId]),
      ...withConv(s, hostId, ev.convKey, (c) => ({ ...c, todos: upsertTodos(c.todos, ev.todos), missing: false })),
    })); set((s) => evicted(s, hostId)) },

    applyRefreshAvailable: (hostId, ev) => { set((s) => withConv(s, hostId, ev.convKey, (c) => ({ ...c, refreshAvailable: ev.available, availAt: ++clock }))); set((s) => evicted(s, hostId)) },

    forgetHost: (hostId) => set((s) => {
      const drop = <T>(m: Record<string, T>): Record<string, T> => { const { [hostId]: _g, ...rest } = m; return rest }
      return {
        byHost: drop(s.byHost), convOfSession: drop(s.convOfSession), support: drop(s.support), gens: drop(s.gens),
        missingSessions: drop(s.missingSessions), seatGen: drop(s.seatGen), viewing: drop(s.viewing), epoch: { ...s.epoch, [hostId]: (s.epoch[hostId] ?? 0) + 1 },
      }
    }),

    reset: () => { inflight.clear(); set({ byHost: {}, convOfSession: {}, support: {}, gens: {}, missingSessions: {}, seatGen: {}, viewing: {}, epoch: {} }) },
  }
})
