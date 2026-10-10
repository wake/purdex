// spa/src/lib/workbook/merge.ts — the pure merge and retention rules of the workbook store (no store, no I/O): entries
// by id, and the todo book (events upsert, a conversation answer is a snapshot, retention bounds). The store holds the
// state and the fetches; the rules live here so they can be tested on their own.
import type { TodoLists, WorkbookEntry, WorkbookTodo } from './types'

export const MAX_OPEN_TODOS = 50
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

export const emptyTodos = (): TodoBook => ({ open: [], done: [], doneOldestId: null, doneCursor: null, doneExhausted: false, doneCapped: false, loading: false, touches: [], touchFloor: 0 })

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
  if (openList.length > MAX_OPEN_TODOS) openList = openList.slice(openList.length - MAX_OPEN_TODOS)
  let doneList = [...done.values()].sort((a, b) => b.id - a.id)
  let doneCapped = t.doneCapped
  if (doneList.length > MAX_DONE_TODOS) { doneList = doneList.slice(0, MAX_DONE_TODOS); doneCapped = true }
  return {
    ...t, open: openList, done: doneList, doneCapped,
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

/** A conversation answer's todos are a snapshot as of when the daemon answered, which is after `startedAt` (the stamp of
 *  its request) — and an event that landed after `startedAt` can be newer than it. Monotonic by construction: its done
 *  list is always safe to merge (done is terminal); its open list is replaced into the book except (1) the ids an event
 *  touched after `startedAt` (the event wins) and (2) todos newer than anything in it. If the touch history reaches
 *  past `startedAt` (the bound discarded it), the answer's open list cannot be vouched for and is ignored. */
export function snapshotTodos(t: TodoBook, snap: TodoLists, startedAt: number): TodoBook {
  if (startedAt < t.touchFloor) return upsertTodos(t, snap.done)
  const newer = new Set(t.touches.filter((x) => x.at > startedAt).map((x) => x.id))
  const newest = Math.max(0, ...snap.open.map((x) => x.id), ...snap.done.map((x) => x.id))
  const base = { ...t, open: t.open.filter((x) => x.id > newest || newer.has(x.id)) }
  return upsertTodos(upsertTodos(base, snap.done.filter((x) => !newer.has(x.id))), snap.open.filter((x) => !newer.has(x.id)))
}

