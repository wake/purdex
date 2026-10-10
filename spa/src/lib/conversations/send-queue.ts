// spa/src/lib/conversations/send-queue.ts — the App-side send queue (U3 plan D7; iOS `SendQueue` ported): a 3 s undo window,
// ONE serial chain (a message goes out only after the one before it settled), a local echo (「你 · 排隊中」) until the
// transcript's user item for it appears, and `busy` kept in the App until the agent is idle. Framework-free; the port is
// injected. Module-level registry per pane so the queue outlives the component (tab-hosted rule).
//
// At most once: every message has ONE client_msg_id, reused by any resend. Only `busy` is resent by the queue itself.
// `unknown`, a lost connection and `not_owner` leave the entry `maybe`: it is settled by the transcript (the user item
// carries the client_msg_id, or the same text within 30 s) and otherwise stays 「可能已送出」 until the person decides.
import { mayHaveRun, newClientMsgId, type SendOutcome, type SendPort } from './send'
import { normalizePrompt } from './send-plan'
import type { UserItem } from './types'

export const UNDO_MS = 3000
// The daemon's echo pairing window (internal/promptq/echo.go): a row may be stamped 2 s before the hand-out, up to 30 s after
// a result that never came (the hand timeout, 10 s, plus 30 s).
const ECHO_BEFORE_MS = 2000
const ECHO_AFTER_MS = 30_000
const HAND_TIMEOUT_MS = 10_000
// The mod may keep answering busy for about a second after the header went idle: a busy answer while the queue believes the
// agent is idle is retried after 1 s, 2 s, 4 s (same client_msg_id: busy means the daemon did not run it), then it waits for the
// next idle edge.
const BACKOFF_BASE_MS = 1000
const BACKOFF_MAX = 3

export type EntryState = 'undo' | 'sending' | 'waiting' | 'sent' | 'maybe' | 'failed' | 'superseded'

export interface QueueEntry {
  /** The client_msg_id. */
  id: string
  text: string
  state: EntryState
  createdAt: number
  undoUntil: number
  /** When the latest submit started (the text-match window is around it). */
  startedAt: number
  /** When the latest submit got its answer. */
  settledAt?: number
  outcome?: SendOutcome
  /** The id of the message that replaced this one after a manual resend (the old entry is kept for the audit trail). */
  supersededBy?: string
  /** An idle was observed while THIS entry's request was in flight and not yet used: its busy answer is resent at once (once). Gone with the entry. */
  idleSeen?: boolean
  /** Backoff retries used since the last idle edge. */
  retries?: number
  /** The one wake-up request after the quota ran out (a remount) has been used; a real idle edge gives it back. */
  woken?: boolean
}

export class SendQueue {
  private list: QueueEntry[] = []
  private view: readonly QueueEntry[] = []
  private timer: ReturnType<typeof setTimeout> | null = null
  private claimed = new Set<string>()
  private disposed = false
  /** The latest observation of the header (`setIdle`). */
  private idleNow = false
  /** At most one: only the head waits, and only it can be backing off. */
  private backoff: { id: string; timer: ReturnType<typeof setTimeout> } | null = null
  private retiredDone: (() => void) | null = null
  private listeners = new Set<() => void>()

  private readonly port: SendPort
  private readonly now: () => number

  constructor(port: SendPort, now: () => number = Date.now) {
    this.port = port
    this.now = now
  }

  subscribe = (fn: () => void): (() => void) => { this.listeners.add(fn); return () => { this.listeners.delete(fn) } }
  entries = (): readonly QueueEntry[] => this.view

  private clearBackoff(): void {
    if (this.backoff) clearTimeout(this.backoff.timer)
    this.backoff = null
  }

  private emit(): void {
    // the backoff belongs to a message that is still waiting: any way out of it (undo, dismiss, echo, resend) takes the timer too
    if (this.backoff && !this.list.some((e) => e.id === this.backoff!.id && e.state === 'waiting')) this.clearBackoff()
    this.view = this.list.map((e) => ({ ...e }))
    this.listeners.forEach((l) => l())
  }

  /** Queue a message (already planned / normalized). Returns its client_msg_id. */
  enqueue(text: string): string {
    const at = this.now()
    const id = newClientMsgId()
    this.list.push({ id, text, state: 'undo', createdAt: at, undoUntil: at + UNDO_MS, startedAt: at })
    this.emit()
    this.pump()
    return id
  }

  /** Take a message back inside its undo window (or cancel one waiting for idle): returns its text for the box. */
  undo(id: string): string | undefined {
    const e = this.list.find((x) => x.id === id)
    if (!e || (e.state !== 'undo' && e.state !== 'waiting')) return undefined
    this.list = this.list.filter((x) => x !== e)
    this.emit()
    this.pump()
    return e.text
  }

  /**
   * The person asks to send a `maybe` / `failed` entry again. It is a NEW message with a NEW client_msg_id: the daemon's
   * ledger is in memory with a TTL, so the old id may be replayed (a cached `dropped`) or forgotten (a restart: the request
   * would run a second time under the old id). The old entry stays, marked superseded. The caller confirms first for a
   * `maybe` (it may have run). Returns the new id.
   */
  resend(id: string): string | undefined {
    const i = this.list.findIndex((x) => x.id === id)
    const e = this.list[i]
    if (!e || (e.state !== 'maybe' && e.state !== 'failed')) return undefined
    const at = this.now()
    const next = newClientMsgId()
    e.state = 'superseded'
    e.supersededBy = next
    this.list.splice(i + 1, 0, { id: next, text: e.text, state: 'undo', createdAt: at, undoUntil: at, startedAt: at })
    this.emit()
    this.pump()
    return next
  }

  dismiss(id: string): void {
    this.list = this.list.filter((x) => x.id !== id || x.state === 'sending')
    this.emit()
  }

  /**
   * An observation of the header status. A message the mod refused as `busy` is resent ONCE by the first idle observed after
   * the busy answer (not by an edge: the pane may have been unmounted when the answer came, so the remount reports an
   * idle that never changed). The caller reports on change and on mount, so a stale header cannot loop: the entry goes
   * back to waiting if the mod says busy again, and needs a new observation.
   */
  setIdle(idle: boolean): void {
    const edge = idle && !this.idleNow // false -> true; the pane reports the SAME value again on every remount, which is no edge
    this.idleNow = idle
    if (!idle) { for (const e of this.list) e.idleSeen = false; this.clearBackoff(); return }
    const w = this.list.find((e) => e.state === 'waiting') // a waiting entry was refused before this observation, so it is "after the busy answer"
    if (w) {
      if (edge) { this.clearBackoff(); w.retries = 0; w.woken = false } // a real edge: resend now, with a fresh quota
      else if (this.backoff === null && (w.retries ?? 0) >= BACKOFF_MAX && !w.woken) w.woken = true // a remount over a spent quota: ONE request, no refill
      else return // same idle again: a pending backoff keeps its schedule, a spent wake-up stays spent
      w.state = 'undo'; w.undoUntil = this.now(); this.emit(); this.pump(); return
    }
    // The observation came while a request is still out (a pane remounted mid-flight): when that request answers busy this idle
    // is "after the request started", so the answer is resent at once instead of waiting for an edge that will not come.
    const flying = this.list.find((e) => e.state === 'sending')
    if (flying) flying.idleSeen = true
  }

  /** The transcript's user messages: an entry whose echo is there is done (the transcript shows it from now on). */
  reconcile(items: readonly UserItem[]): void {
    const users = items.filter((i) => i.source === 'user' && !this.claimed.has(i.id))
    const open = this.list.filter((e) => e.state === 'sent' || e.state === 'maybe' || e.state === 'sending')
    const done = new Set<QueueEntry>()
    const take = (e: QueueEntry, u: UserItem) => { this.claimed.add(u.id); done.add(e) }
    // 1. an item that carries a client_msg_id is paired by that id and by nothing else (the daemon already paired it)
    for (const u of users) {
      const e = u.client_msg_id !== undefined ? open.find((x) => x.id === u.client_msg_id && !done.has(x)) : undefined
      if (e) take(e, u)
    }
    // 2. the rest by text, in the daemon's window (2 s before the request, 30 s after it was handed out), nearest first.
    // A message still `sending` is never settled this way: only its own id or its transport outcome can.
    const pairs: Array<{ e: QueueEntry; u: UserItem; d: number }> = []
    for (const e of open) {
      if (e.state === 'sending' || done.has(e)) continue
      const to = e.state === 'sent' ? (e.settledAt ?? e.startedAt) + ECHO_BEFORE_MS : e.startedAt + HAND_TIMEOUT_MS + ECHO_AFTER_MS
      for (const u of users) {
        if (this.claimed.has(u.id) || u.client_msg_id !== undefined) continue
        if (u.at < e.startedAt - ECHO_BEFORE_MS || u.at > to || normalizePrompt(u.text) !== e.text) continue
        pairs.push({ e, u, d: Math.abs(u.at - e.startedAt) })
      }
    }
    pairs.sort((a, b) => a.d - b.d)
    for (const p of pairs) if (!done.has(p.e) && !this.claimed.has(p.u.id)) take(p.e, p.u)
    if (done.size === 0) return
    this.list = this.list.filter((x) => !done.has(x))
    this.emit()
    this.pump()
  }

  interrupt(): Promise<SendOutcome> { return this.port.interrupt() }

  /**
   * The queue's conversation will not be shown again (the pane moved to another session): everything not yet handed out (undo
   * window, waiting for idle, a settled entry) is dropped, since the old text must never reach another session and nobody is
   * left to drive it. A request already in flight is let run; when its answer lands the queue disposes itself and calls
   * `onDone` (at once when nothing is in flight).
   */
  retire(onDone: () => void): void {
    this.list = this.list.filter((e) => e.state === 'sending')
    this.clearBackoff()
    if (this.timer) clearTimeout(this.timer)
    this.timer = null
    this.listeners.clear()
    if (this.list.length === 0) { this.dispose(); onDone(); return }
    this.retiredDone = onDone
    this.view = this.list.map((e) => ({ ...e }))
  }

  /**
   * The pane is gone for good: no timer, no further request, nothing emitted. A message already handed out (`sending`) keeps
   * whatever the daemon makes of it and its answer is ignored; one still in its undo window or waiting for idle is dropped.
   */
  dispose(): void {
    this.disposed = true
    this.clearBackoff()
    this.list = []
    this.view = []
    if (this.timer) clearTimeout(this.timer)
    this.timer = null
    this.listeners.clear()
  }

  /** The backoff timer fired: resend the waiting message if the header is still idle (one request at a time, same id). */
  private retryBusy(e: QueueEntry): void {
    this.backoff = null
    if (this.disposed || this.retiredDone || !this.idleNow || e.state !== 'waiting' || !this.list.includes(e)) return
    e.state = 'undo'
    e.undoUntil = this.now()
    this.emit()
    this.pump()
  }

  // One at a time: the first entry that has not settled is the head.
  private pump(): void {
    if (this.disposed || this.retiredDone) return
    if (this.timer) { clearTimeout(this.timer); this.timer = null }
    const head = this.list.find((e) => e.state === 'undo' || e.state === 'sending' || e.state === 'waiting')
    if (!head || head.state !== 'undo') return
    const left = head.undoUntil - this.now()
    if (left > 0) { this.timer = setTimeout(() => this.pump(), left); return }
    head.state = 'sending'
    head.startedAt = this.now()
    this.emit()
    void this.port.submit(head.text, head.id).then((o) => this.settle(head, o))
  }

  private settle(e: QueueEntry, o: SendOutcome): void {
    if (this.disposed) return
    if (this.retiredDone) {
      // retired: the answer changes nothing and starts nothing; the last one in flight frees the queue
      this.list = this.list.filter((x) => x !== e)
      if (this.list.every((x) => x.state !== 'sending')) { const done = this.retiredDone; this.dispose(); done() }
      return
    }
    if (e.state !== 'sending' || !this.list.includes(e)) return // already echoed by the transcript
    e.outcome = o
    e.settledAt = this.now()
    const seenIdle = e.idleSeen === true
    e.idleSeen = false // used by this answer or stale after it: one observation, at most one resend
    if (o.kind === 'accepted') e.state = 'sent'
    else if (o.kind === 'busy') {
      e.state = 'waiting'
      if (seenIdle) { e.state = 'undo'; e.undoUntil = this.now() }
      else if (this.idleNow && (e.retries ?? 0) < BACKOFF_MAX) {
        const delay = BACKOFF_BASE_MS * 2 ** (e.retries ?? 0)
        e.retries = (e.retries ?? 0) + 1
        this.clearBackoff()
        this.backoff = { id: e.id, timer: setTimeout(() => this.retryBusy(e), delay) }
      }
    }
    else if (mayHaveRun(o)) e.state = 'maybe'
    else {
      e.state = 'failed'
      if (o.kind === 'no_mod') {
        for (const x of this.list) if (x.state === 'undo' || x.state === 'waiting') { x.state = 'failed'; x.outcome = o }
      }
    }
    this.emit()
    this.pump()
  }
}

const queues = new Map<string, SendQueue>()

/** The pane's queue: it survives the component (a tab switch unmounts the pane). */
export function sendQueueFor(key: string, make: () => SendPort): SendQueue {
  let q = queues.get(key)
  if (!q) { q = new SendQueue(make()); queues.set(key, q) }
  return q
}

/** Whether a queue is held under this key (the registry's size is `sendQueueCount`). */
export const hasSendQueue = (key: string): boolean => queues.has(key)
export const sendQueueCount = (): number => queues.size

/** Dispose and forget the queues whose key matches (a pane that is gone for good); returns how many were released. */
export function releaseSendQueues(match: (key: string) => boolean): number {
  let n = 0
  for (const [key, q] of [...queues]) {
    if (!match(key)) continue
    q.dispose()
    queues.delete(key)
    n++
  }
  return n
}

let retiring = 0
/** Retired queues still waiting for a request in flight (they are no longer in the registry). */
export const retiringQueueCount = (): number => retiring

/**
 * Retire the queues whose key matches (the pane shows another session now): see `SendQueue.retire`. Each is detached from the
 * registry AT ONCE, so the next `sendQueueFor` of the same key (the pane flips back) is a fresh queue; the old one only waits
 * for its request in flight and then disposes itself.
 */
export function retireSendQueues(match: (key: string) => boolean): void {
  for (const [key, q] of [...queues]) {
    if (!match(key)) continue
    queues.delete(key)
    retiring++
    q.retire(() => { retiring-- })
  }
}

/** Tests only. */
export function clearAllSendQueues(): void {
  retiring = 0
  queues.forEach((q) => q.dispose())
  queues.clear()
}
