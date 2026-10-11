// spa/src/lib/conversations/take-back-machine.ts — the take-back's decisions, pure (#2682). take-back.ts kept listeners, timers, the header snapshot,
// draft writes, send-queue dismissals and adapter calls in one module, so the races of #2681 (which answer is still wanted, which header rows an answer
// stands for, when the optimistic hide ends) were tested mostly through large component tests. Here they are values and functions of values:
//
// - a per-key `TakeBackCore`: the visible state plus the *generation* of the take-back in flight (null when idle). A take-back runs under the
//   generation it started with; letting the key go (pane released, session changed) drops it, and an answer that arrives under a generation that is
//   no longer current applies nothing — whether a later take-back has started since or not;
// - the header-row matching: which (text, at) *instances* an answer came back for, which of the App's entries stand for them, and when the hide of
//   those rows is over (the header stopped listing them, or the timer, which only ends the hide it was set for).
//
// take-back.ts is the thin coordinator: it owns the maps, the timers, the listeners and every side effect, and asks this module what to do.
// Pure: no React, no store, no timer, no clock (the caller passes `now`).
import type { OutcomeMessage } from './send'
import type { HeaderRow, QueueEntry } from './send-queue'

/** Header rows the answer brought back stay hidden at most this long: `header.queued` takes about a second to empty after a recall. */
export const HIDE_MS = 5000

export interface TakeBackState {
  /** A take-back is on its way: further Up / clicks are ignored and the rows show a spinner. */
  busy: boolean
  /** Why nothing came back (dismissable); drawn by the pending tail. */
  notice: OutcomeMessage | null
  /** The header rows (text, at) that came back: exactly those instances are hidden until the header stops listing them (or HIDE_MS pass). */
  hide: { rows: readonly HeaderRow[]; at: number } | null
}

export const IDLE: TakeBackState = { busy: false, notice: null, hide: null }

/** A key's take-back: its state, and the generation in flight (only while `state.busy` and the answer is still wanted). */
export interface TakeBackCore {
  state: TakeBackState
  op: number | null
}

export const IDLE_CORE: TakeBackCore = { state: IDLE, op: null }

/** Whether a core holds anything: an idle one is not kept. */
export const isIdle = (c: TakeBackCore): boolean => !c.state.busy && !c.state.notice && !c.state.hide && c.op === null

const withState = (c: TakeBackCore, patch: Partial<TakeBackState>): TakeBackCore => ({ ...c, state: { ...c.state, ...patch } })

// === The operation (generation) ===

/**
 * Start a take-back under `gen`, or `null` when one is on its way (busy): further Up / clicks are ignored. Clears the last notice.
 * `gen` is the caller's own counter and only ever grows, across keys being forgotten too — a generation is never handed out twice.
 */
export function begin(c: TakeBackCore, gen: number): TakeBackCore | null {
  if (c.state.busy) return null
  return { ...withState(c, { busy: true, notice: null }), op: gen }
}

/** Whether `gen` is still the take-back this key wants the answer of. */
export const isCurrent = (c: TakeBackCore, gen: number): boolean => c.op === gen

/** The answer said nothing came back: the take-back is over and the notice says why. `null` when the answer is stale. */
export function fail(c: TakeBackCore, gen: number, notice: OutcomeMessage): TakeBackCore | null {
  if (!isCurrent(c, gen)) return null
  return { ...withState(c, { busy: false, notice }), op: null }
}

/**
 * The text came back (the caller has put it in the draft): the take-back is no longer in flight, and the hide of an earlier one is over — its rows
 * were just taken back again. Still `busy` until the caller says which rows this one stands for (`settle`). `null` when the answer is stale.
 */
export function land(c: TakeBackCore, gen: number): TakeBackCore | null {
  if (!isCurrent(c, gen)) return null
  return { state: c.state.hide ? { ...c.state, hide: null } : c.state, op: null }
}

/** The take-back is done: `hide` is the header rows that came back (null: none known). */
export function settle(c: TakeBackCore, hide: { rows: readonly HeaderRow[]; at: number } | null): TakeBackCore {
  return withState(c, { busy: false, hide })
}

export function dismissNotice(c: TakeBackCore): TakeBackCore {
  return c.state.notice ? withState(c, { notice: null }) : c
}

// === The optimistic hide of header rows ===

const trimmed = (text: string): string => text.trimEnd()

/** Whether the header row is hidden: it came back a moment ago and the header has not caught up. */
export function isRowHidden(s: TakeBackState, row: HeaderRow, now: number): boolean {
  return !!s.hide && now - s.hide.at < HIDE_MS && s.hide.rows.some((x) => x.at === row.at && x.text === trimmed(row.text))
}

/** How many of `rows` are shown right now (not counting rows hidden after a take-back). */
export function visibleRows(s: TakeBackState, rows: readonly HeaderRow[], now: number): number {
  return rows.filter((x) => !isRowHidden(s, x, now)).length
}

/** Whether the header has stopped listing every row that came back (by instance, (text, at)): the hide has done its job. */
export function headerCaughtUp(s: TakeBackState, header: readonly HeaderRow[]): boolean {
  const h = s.hide
  return !!h && !header.some((r) => h.rows.some((x) => x.at === r.at && x.text === trimmed(r.text)))
}

/** The timer of the hide set at `at` ran out: it ends that hide only (a newer one, set since, has its own timer). */
export function expireHide(c: TakeBackCore, at: number): TakeBackCore {
  return c.state.hide?.at === at ? withState(c, { hide: null }) : c
}

// === Which lines an answer stands for ===

/** What Claude Code's queue holds as the pane renders it, in reading order. */
export interface QueueSnapshot {
  /** The header `user` rows (trailing whitespace trimmed). */
  snapshot: readonly HeaderRow[]
  /** The handed entries no row stands for yet. */
  loose: readonly { id: string; text: string }[]
  /** The daemon joins its queue the same way (typed_recall.go joinQueued), so the answer can be matched against this. */
  expected: string
}

type EntryFacts = Pick<QueueEntry, 'id' | 'state' | 'mode' | 'claimAt' | 'text'>

/** An entry the daemon holds on Claude Code's queue: handed over and not yet echoed. */
const handed = (e: EntryFacts): boolean => e.state === 'sent' && e.mode !== undefined

/** The header rows (an entry that claimed one stands for it), then the handed entries no row stands for yet. */
export function snapshotOf(header: readonly HeaderRow[], entries: readonly EntryFacts[]): QueueSnapshot {
  const snapshot = header.map((r) => ({ at: r.at, text: trimmed(r.text) }))
  const loose = entries.filter((e) => handed(e) && e.claimAt === undefined).map((e) => ({ id: e.id, text: trimmed(e.text) }))
  return { snapshot, loose, expected: [...snapshot.map((r) => r.text), ...loose.map((e) => e.text)].join('\n') }
}

/** Whether the answer's text is exactly the queue as it was snapshotted (else the queue changed meanwhile: which rows came back is unknown). */
export const answerMatches = (q: QueueSnapshot, text: string): boolean => q.expected !== '' && text === q.expected

/**
 * The entries that stood for the rows that came back, one to one with the snapshot: an entry holding a claim on one of its rows (each row at most
 * once), or one that stood alone. Their ids, in queue order.
 */
export function entriesToDismiss(q: QueueSnapshot, entries: readonly EntryFacts[]): string[] {
  const free = [...q.snapshot]
  const ids: string[] = []
  for (const e of entries) {
    if (!handed(e)) continue
    const k = e.claimAt === undefined ? -1 : free.findIndex((r) => r.at === e.claimAt && r.text === trimmed(e.text))
    if (k >= 0) { free.splice(k, 1); ids.push(e.id) } else if (q.loose.some((l) => l.id === e.id)) ids.push(e.id)
  }
  return ids
}

/** The hide an answer leaves: the snapshot's rows (none: no hide). */
export function hideAfter(q: QueueSnapshot, at: number): TakeBackState['hide'] {
  return q.snapshot.length > 0 ? { rows: q.snapshot, at } : null
}
