// spa/src/lib/conversations/take-back.ts — taking a sent message back (K-2b, plan D7): the App's held block (no daemon call), and what Claude Code
// queued (POST /recall through the bound adapter). The state lives here, not in a component: a take-back answered while the tab is switched away
// still lands in the draft memory, and the in-flight guard, the notice and the optimistic hide survive the pane's unmount.
//
// This module is the coordinator (#2682): the maps, the listeners, the hide timers, and every side effect — the adapter call, the draft write, the
// send-queue dismissals. What to do is `take-back-machine.ts`'s question (generation of the take-back in flight, which header rows an answer stands
// for, when the hide is over), asked of plain values and unit-tested as tables.
import { useSyncExternalStore } from 'react'
import { adapterOf } from './deck-adapter'
import { prependToDraft, recallHeld } from './draft-recall'
import { recallMessage } from './send'
import type { HeaderRow, SendQueue } from './send-queue'
import {
  answerMatches, begin, dismissNotice, entriesToDismiss, expireHide, fail, headerCaughtUp, hideAfter, IDLE_CORE, isCurrent, isIdle, isRowHidden as hidden, land,
  settle, snapshotOf, visibleRows, HIDE_MS, type TakeBackCore, type TakeBackState,
} from './take-back-machine'

export { HIDE_MS }
export type { TakeBackState }

const cores = new Map<string, TakeBackCore>()
const rows = new Map<string, readonly HeaderRow[]>()
/** Only ever grows (not even a forgotten key starts over): a generation is never handed out twice, so a late answer cannot be taken for a newer take-back's. */
let generation = 0
/** Tests only: how many keys have a take-back in flight. */
export const inflightTakeBackCount = (): number => [...cores.values()].filter((c) => c.op !== null).length
const timers = new Map<string, ReturnType<typeof setTimeout>>()
const listeners = new Map<string, Set<() => void>>()

const coreOf = (key: string): TakeBackCore => cores.get(key) ?? IDLE_CORE
const get = (key: string): TakeBackState => coreOf(key).state

/** Keeps `next` (an idle key holds nothing) and tells the key's listeners when what they draw changed. */
function commit(key: string, next: TakeBackCore): void {
  const changed = next.state !== get(key)
  if (isIdle(next)) cores.delete(key)
  else cores.set(key, next)
  if (changed) for (const l of [...(listeners.get(key) ?? [])]) l()
}

function subscribe(key: string, cb: () => void): () => void {
  let s = listeners.get(key)
  if (!s) listeners.set(key, (s = new Set()))
  s.add(cb)
  return () => { s.delete(cb); if (s.size === 0 && listeners.get(key) === s) listeners.delete(key) }
}

/** The take-back state of the pane's conversation, live. */
export function useTakeBackState(key: string): TakeBackState {
  return useSyncExternalStore((cb) => subscribe(key, cb), () => get(key))
}

export const dismissTakeBackNotice = (key: string): void => { commit(key, dismissNotice(coreOf(key))) }

/** Whether the header row `text` is hidden: it came back a moment ago and the header has not caught up. */
export function isRowHidden(s: TakeBackState, row: HeaderRow, now: number = Date.now()): boolean {
  return hidden(s, row, now)
}

function clearTimer(key: string): void {
  const t = timers.get(key)
  if (t) clearTimeout(t)
  timers.delete(key)
}

function clearHide(key: string): void {
  clearTimer(key)
  const c = coreOf(key)
  if (c.state.hide) commit(key, { ...c, state: { ...c.state, hide: null } })
}

/**
 * The `user` rows of Claude Code's queue line as the deck renders them, reported by the pane's driver on every header change. Once the header
 * lists none of the lines that came back, the optimistic hide has done its job.
 */
export function setHeaderUserRows(key: string, now: readonly HeaderRow[]): void {
  rows.set(key, now)
  if (headerCaughtUp(get(key), now)) clearHide(key)
}

/** How many `user` rows of the header are shown right now (not counting rows hidden after a take-back). */
export function visibleHeaderUsers(key: string): number {
  return visibleRows(get(key), rows.get(key) ?? [], Date.now())
}

/** Whether this pane can ask the daemon to take Claude Code's queued messages back. */
export const canTakeBack = (key: string): boolean => !!adapterOf(key)?.takeBack

/**
 * Take back everything Claude Code queued from this conversation (click on a header row or on a handed entry; the daemon returns all of them at once).
 * `back` prepends the text to the draft, drops the App entries that stand for those lines and hides the header rows until the header catches up.
 * Anything else shows its message. Resolves when the answer has been applied.
 */
export async function takeBackQueued(key: string, queue: SendQueue): Promise<void> {
  const takeBack = adapterOf(key)?.takeBack
  if (!takeBack) return
  const gen = ++generation
  const started = begin(coreOf(key), gen)
  if (!started) return // one is on its way
  // What Claude Code's queue holds as this pane renders it; the answer is matched against it.
  const queued = snapshotOf(rows.get(key) ?? [], queue.entries())
  commit(key, started)
  let outcome
  try { outcome = await takeBack() } catch { outcome = { kind: 'may_have_popped' as const, reason: 'network' } }
  if (outcome.kind !== 'back') {
    const failed = fail(coreOf(key), gen, recallMessage(outcome))
    if (failed) commit(key, failed) // else the pane let this conversation go while the daemon answered: nothing of it applies any more
    return
  }
  if (!isCurrent(coreOf(key), gen)) return // the same: stale
  prependToDraft(key, outcome.text)
  const landed = land(coreOf(key), gen) // (read again: the draft write woke listeners, which may have touched this key)
  if (!landed) return
  clearTimer(key)
  commit(key, landed)
  if (!answerMatches(queued, outcome.text)) { commit(key, settle(coreOf(key), null)); return } // the queue changed meanwhile: which rows came back is unknown
  for (const id of entriesToDismiss(queued, queue.entries())) queue.dismiss(id)
  const at = Date.now()
  timers.set(key, setTimeout(() => { timers.delete(key); commit(key, expireHide(coreOf(key), at)) }, HIDE_MS))
  commit(key, settle(coreOf(key), hideAfter(queued, at)))
}

/** A held entry's block was clicked (or its Undo): that entry only, no daemon call. */
export function takeBackHeld(key: string, queue: SendQueue, id: string): boolean {
  if (get(key).busy) return false
  return recallHeld(key, queue, id)
}

/** Whether Up in an empty box has something to take back: held messages, or Claude Code's queue as the header renders it. */
export const canTakeBackAll = (key: string, queue: SendQueue): boolean => queue.heldCount() > 0 || (canTakeBack(key) && hasQueuedToTakeBack(key, queue))

/** Everything K-2b's daemon take-back can act on: a rendered header `user` row, or an entry handed to the daemon and not yet echoed (claimed a row or not). */
function hasQueuedToTakeBack(key: string, queue: SendQueue): boolean {
  return visibleHeaderUsers(key) > 0 || queue.entries().some((e) => e.state === 'sent' && e.mode !== undefined)
}

/**
 * Up in an empty box: everything pending, oldest first. The held entries come into the box first (they leave the App's hands at once; the send
 * buffer must not run out while the daemon answers), then Claude Code's queue is put in front of them, so the box reads: recalled, then held.
 * A failed take-back still returns the held entries and shows its message.
 */
export async function takeBackAll(key: string, queue: SendQueue): Promise<void> {
  if (get(key).busy) return
  if (queue.heldCount() > 0) recallHeld(key, queue)
  if (canTakeBack(key) && hasQueuedToTakeBack(key, queue)) await takeBackQueued(key, queue)
}

/** A pane that is gone for good, or a session it no longer shows. */
export function forgetTakeBackWhere(match: (key: string) => boolean): void {
  for (const key of [...new Set([...cores.keys(), ...rows.keys(), ...timers.keys()])]) {
    if (!match(key)) continue
    clearTimer(key)
    cores.delete(key) // whatever was in flight is dropped: its answer finds no current generation and applies nothing
    rows.delete(key)
  }
}

/** Tests only: module state outlives a test. */
export const clearAllTakeBack = (): void => { forgetTakeBackWhere(() => true) }
