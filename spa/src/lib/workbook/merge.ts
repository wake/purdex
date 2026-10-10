// spa/src/lib/workbook/merge.ts — the pure merge and retention rules of the workbook store (no store, no I/O): entries
// by id, and the todo book (events upsert, a conversation answer is a snapshot, retention bounds). The store holds the
// state and the fetches; the rules live here so they can be tested on their own.
import type { TodoLists, WorkbookEntry, WorkbookTodo } from './types'

/** Safety ceiling on the open todos kept per conversation. The daemon's own list holds ≤ 30 open (and the answer's `open` is the
 *  whole list, spec §9), so nothing the daemon sends reaches this; only a flood does, and then the oldest are dropped AND
 *  `openCapped` says so. */
export const MAX_OPEN_TODOS = 500
/** A safety ceiling on the done record kept per conversation, not a retention policy (spec §6 keeps every record, §9 pages it,
 *  and the daemon holds them all): ~2000 todos is far above what a conversation's list reaches (the model adds ≤ 2 a turn and
 *  closes most), and only there does the store stop paging (`doneCapped`) and drop the oldest to bound memory. */
export const MAX_DONE_TODOS = 2000
export const MAX_TODO_TOUCHES = 200

export interface TodoBook {
  /** Oldest first. */
  open: WorkbookTodo[]
  /** Newest first. */
  done: WorkbookTodo[]
  /** The open list hit MAX_OPEN_TODOS and the oldest open todos were dropped (never silently: the UI can say so). */
  openCapped: boolean
  /** The oldest done todo RETAINED (null while none is). */
  doneOldestId: number | null
  /** The next page's `before=`: the oldest id the last page brought, kept even when retention dropped that page; null: use `doneOldestId`. */
  doneCursor: number | null
  /** The record has no older page (a short page, or one that did not move the cursor). */
  doneExhausted: boolean
  /** Retention (MAX_DONE_TODOS) dropped older done todos: they are not kept and paging stops (the UI shows the cap, not 「更多」). */
  doneCapped: boolean
  loading: boolean
  /** The todos `workbook.todos` events changed, with the store-clock stamp of the event (newest last, bounded). */
  touches: { id: number; at: number }[]
  /** Stamp up to which `touches` was discarded for the bound: an answer whose request started before it cannot be vouched for. */
  touchFloor: number
}

export const emptyTodos = (): TodoBook => ({ open: [], done: [], openCapped: false, doneOldestId: null, doneCursor: null, doneExhausted: false, doneCapped: false, loading: false, touches: [], touchFloor: 0 })

/** Merge `incoming` into a conversation's entries: newest first, one per id, a stale copy never replaces a newer one. */
export function mergeEntries(have: WorkbookEntry[], incoming: WorkbookEntry[]): WorkbookEntry[] {
  const byId = new Map(have.map((e) => [e.id, e]))
  for (const e of incoming) {
    const cur = byId.get(e.id)
    if (!cur || e.updatedAt >= cur.updatedAt) byId.set(e.id, e)
  }
  return [...byId.values()].sort((a, b) => b.id - a.id)
}

/** Fold `incoming` todos into the book by id. A done todo is never reopened or dropped (events arrive in the daemon's
 *  order, so a closed todo's open copy does not follow its close; the late copies that can are handled by `snapshotTodos`).
 *  Bounded: open keeps the newest, done keeps the newest and sets `doneCapped` when it drops older ones. */
export function upsertTodos(t: TodoBook, incoming: WorkbookTodo[]): TodoBook {
  const open = new Map(t.open.map((x) => [x.id, x]))
  const done = new Map(t.done.map((x) => [x.id, x]))
  for (const x of incoming) {
    if (done.has(x.id) && x.state !== 'done') continue
    if (x.state === 'open') open.set(x.id, x)
    else if (x.state === 'done') { open.delete(x.id); done.set(x.id, x) }
    else open.delete(x.id)
  }
  let openList = [...open.values()].sort((a, b) => a.id - b.id)
  let openCapped = t.openCapped
  if (openList.length > MAX_OPEN_TODOS) { openList = openList.slice(openList.length - MAX_OPEN_TODOS); openCapped = true }
  let doneList = [...done.values()].sort((a, b) => b.id - a.id)
  let doneCapped = t.doneCapped
  if (doneList.length > MAX_DONE_TODOS) { doneList = doneList.slice(0, MAX_DONE_TODOS); doneCapped = true }
  return {
    ...t, open: openList, openCapped, done: doneList, doneCapped,
    doneOldestId: doneList.length ? doneList[doneList.length - 1].id : null,
  }
}

/** An event changed these todos at `at`: remembered (bounded) so a late answer cannot undo them. */
export function touched(t: TodoBook, incoming: WorkbookTodo[], at: number): TodoBook {
  let touches = [...t.touches, ...incoming.map((x) => ({ id: x.id, at }))]
  let touchFloor = t.touchFloor
  if (touches.length > MAX_TODO_TOUCHES) {
    const cut = touches.length - MAX_TODO_TOUCHES
    touchFloor = Math.max(touchFloor, touches[cut - 1].at)
    touches = touches.slice(cut)
  }
  return { ...t, touches, touchFloor }
}

/** Whether an answer whose request started at `startedAt` can be reconciled with the events seen since: false once the touch
 *  history (bounded) no longer reaches back to it. */
export const snapshotTrusted = (t: TodoBook, startedAt: number): boolean => startedAt >= t.touchFloor

/** A conversation answer's `open` is the daemon's whole open list as of when it answered, which is after `startedAt` (the
 *  stamp of its request, spec §9). So every local todo from before the request is decided by the answer; the only local state
 *  that survives is what an event touched AFTER `startedAt` (the event is newer than the answer). Its done list is always safe
 *  to merge (done is terminal). If the touch history no longer reaches back to `startedAt` the answer's open list cannot be
 *  reconciled with the events since: it is not applied (`trusted: false`, local open kept as it is) and the caller re-asks. */
export function snapshotTodos(t: TodoBook, snap: TodoLists, startedAt: number): { book: TodoBook; trusted: boolean } {
  if (!snapshotTrusted(t, startedAt)) return { book: upsertTodos(t, snap.done), trusted: false }
  const newer = new Set(t.touches.filter((x) => x.at > startedAt).map((x) => x.id))
  const base = { ...t, open: t.open.filter((x) => newer.has(x.id)) }
  return { book: upsertTodos(upsertTodos(base, snap.done.filter((x) => !newer.has(x.id))), snap.open.filter((x) => !newer.has(x.id))), trusted: true }
}
