// spa/src/lib/conversations/take-back.ts — taking a sent message back (K-2b, plan D7): the App's held block (no daemon call), and what Claude Code
// queued (POST /recall through the bound adapter). The state lives here, not in a component: a take-back answered while the tab is switched away
// still lands in the draft memory, and the in-flight guard, the notice and the optimistic hide survive the pane's unmount.
import { useSyncExternalStore } from 'react'
import { adapterOf } from './deck-adapter'
import { prependToDraft, recallHeld } from './draft-recall'
import { recallMessage, type OutcomeMessage } from './send'
import type { SendQueue } from './send-queue'

/** Header rows the answer brought back stay hidden at most this long: `header.queued` takes about a second to empty after a recall. */
export const HIDE_MS = 5000

export interface TakeBackState {
  /** A take-back is on its way: further Up / clicks are ignored and the rows show a spinner. */
  busy: boolean
  /** Why nothing came back (dismissable); drawn by the pending tail. */
  notice: OutcomeMessage | null
  /** The lines that came back: the header rows with this text are hidden until the header stops listing them (or HIDE_MS pass). */
  hide: { lines: readonly string[]; at: number } | null
}

const EMPTY: TakeBackState = { busy: false, notice: null, hide: null }
const states = new Map<string, TakeBackState>()
const rows = new Map<string, readonly string[]>()
const timers = new Map<string, ReturnType<typeof setTimeout>>()
const listeners = new Map<string, Set<() => void>>()

const get = (key: string): TakeBackState => states.get(key) ?? EMPTY

function set(key: string, patch: Partial<TakeBackState>): void {
  const next = { ...get(key), ...patch }
  if (!next.busy && !next.notice && !next.hide) states.delete(key)
  else states.set(key, next)
  for (const l of [...(listeners.get(key) ?? [])]) l()
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

export const dismissTakeBackNotice = (key: string): void => { if (get(key).notice) set(key, { notice: null }) }

const trimmed = (text: string): string => text.trimEnd()

/** Whether the header row `text` is hidden: it came back a moment ago and the header has not caught up. */
export function isRowHidden(s: TakeBackState, text: string, now: number = Date.now()): boolean {
  return !!s.hide && now - s.hide.at < HIDE_MS && s.hide.lines.includes(trimmed(text))
}

function clearHide(key: string): void {
  const t = timers.get(key)
  if (t) clearTimeout(t)
  timers.delete(key)
  if (get(key).hide) set(key, { hide: null })
}

/**
 * The `user` rows of Claude Code's queue line as the deck renders them, reported by the pane's driver on every header change. Once the header
 * lists none of the lines that came back, the optimistic hide has done its job.
 */
export function setHeaderUserRows(key: string, texts: readonly string[]): void {
  rows.set(key, texts)
  const h = get(key).hide
  if (h && !texts.some((x) => h.lines.includes(trimmed(x)))) clearHide(key)
}

/** How many `user` rows of the header are shown right now (not counting rows hidden after a take-back). */
export function visibleHeaderUsers(key: string): number {
  const s = get(key)
  return (rows.get(key) ?? []).filter((x) => !isRowHidden(s, x)).length
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
  if (!takeBack || get(key).busy) return
  set(key, { busy: true, notice: null })
  let outcome
  try { outcome = await takeBack() } catch { outcome = { kind: 'may_have_popped' as const, reason: 'network' } }
  if (outcome.kind !== 'back') { set(key, { busy: false, notice: recallMessage(outcome) }); return }
  const lines = outcome.text.split('\n').map(trimmed).filter((x) => x !== '')
  prependToDraft(key, outcome.text)
  for (const e of queue.entries()) if (e.state === 'sent' && e.mode !== undefined && lines.includes(trimmed(e.text))) queue.dismiss(e.id)
  clearHide(key)
  const at = Date.now()
  timers.set(key, setTimeout(() => { timers.delete(key); if (get(key).hide?.at === at) set(key, { hide: null }) }, HIDE_MS))
  set(key, { busy: false, hide: { lines, at } })
}

/** A held entry's block was clicked (or its Undo): that entry only, no daemon call. */
export function takeBackHeld(key: string, queue: SendQueue, id: string): boolean {
  if (get(key).busy) return false
  return recallHeld(key, queue, id)
}

/** Whether Up in an empty box has something to take back: held messages, or Claude Code's queue as the header renders it. */
export const canTakeBackAll = (key: string, queue: SendQueue): boolean => queue.heldCount() > 0 || (canTakeBack(key) && visibleHeaderUsers(key) > 0)

/**
 * Up in an empty box: everything pending, oldest first. The held entries come into the box first (they leave the App's hands at once; the send
 * buffer must not run out while the daemon answers), then Claude Code's queue is put in front of them, so the box reads: recalled, then held.
 * A failed take-back still returns the held entries and shows its message.
 */
export async function takeBackAll(key: string, queue: SendQueue): Promise<void> {
  if (get(key).busy) return
  if (queue.heldCount() > 0) recallHeld(key, queue)
  if (canTakeBack(key) && visibleHeaderUsers(key) > 0) await takeBackQueued(key, queue)
}

/** A pane that is gone for good, or a session it no longer shows. */
export function forgetTakeBackWhere(match: (key: string) => boolean): void {
  for (const key of [...new Set([...states.keys(), ...rows.keys(), ...timers.keys()])]) {
    if (!match(key)) continue
    const t = timers.get(key)
    if (t) clearTimeout(t)
    timers.delete(key); states.delete(key); rows.delete(key)
  }
}

/** Tests only: module state outlives a test. */
export const clearAllTakeBack = (): void => { forgetTakeBackWhere(() => true) }
