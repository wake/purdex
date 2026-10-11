// spa/src/lib/conversations/send-queue.ts — the App-side send queue (U3 plan D7; K-2 plan D3–D6): a 1.5 s send buffer (`undo`), ONE serial
// chain (a message goes out only after the one before it settled), and a local echo (framed 「你 · 排隊中」) until the transcript's user item
// for it appears. The App does not wait for the agent to be idle: Claude Code queues a busy session's message itself. Framework-free; the
// port is injected. Module-level registry per pane so the queue outlives the component (tab-hosted rule).
//
// At most once: a message has ONE client_msg_id per attempt. Only a `busy` refusal (nothing was sent) is retried by the queue itself, 1 s /
// 2 s / 4 s, each attempt under a NEW id (the daemon records a fill refusal under its id and replays it); the entry keeps every id it used
// and an echo carrying any of them settles it. `unknown`, a lost connection and `text_left_in_terminal` leave the entry `maybe`: it is
// settled by the transcript and otherwise stays 「可能已送出」 until the person decides.
import { mayHaveRun, newClientMsgId, type SendOutcome, type SendPort } from './send'
import { revokePreviewUrl, type Attachment } from './attachment-memory'
import { adapterOf } from './deck-adapter'
import { normalizePrompt } from './send-plan'
import type { UserItem } from './types'

export const UNDO_MS = 1500
// The daemon's echo pairing window (internal/promptq/echo.go): a row may be stamped 2 s before the hand-out, up to 30 s after
// a result that never came (the hand timeout, 10 s, plus 30 s).
const ECHO_BEFORE_MS = 2000
const ECHO_AFTER_MS = 30_000
const HAND_TIMEOUT_MS = 10_000
// A `busy` refusal (the pane / session lock, or Claude Code's queue is full for a moment) did not send anything: retried after 1 s, 2 s, 4 s.
const BUSY_RETRY_BASE_MS = 1000
const BUSY_RETRIES = 3
// A message the daemon handed over (`handed`) that is neither on Claude Code's queue line nor echoed by the transcript after this long is `maybe`.
const HANDED_SEEN_MS = 10_000

/** What the box held when the message was sent: the draft as typed (`[Image #N]` placeholders and all) and its chips (previews still alive). */
export interface HeldDraft {
  draft: string
  chips: readonly Attachment[]
}

export type EntryState = 'undo' | 'sending' | 'sent' | 'maybe' | 'failed' | 'superseded'

export interface QueueEntry {
  /** The entry's identity (its first client_msg_id). */
  id: string
  /** Every client_msg_id this entry used, oldest first (a busy retry takes a new one); the last is the one in use. An echo carrying any of them settles it. */
  ids: string[]
  text: string
  state: EntryState
  createdAt: number
  undoUntil: number
  /** When the latest submit started (the text-match window is around it). */
  startedAt: number
  /** When the latest submit got its answer. */
  settledAt?: number
  outcome?: SendOutcome
  /** The box as it was when this was queued, kept while the message can still come back (the send buffer, in flight): what a recall puts back. Let go when the daemon takes the message or it is dropped. */
  held?: HeldDraft
  /** The id of the message that replaced this one after a manual resend (the old entry is kept for the audit trail). */
  supersededBy?: string
  /** Busy retries used. */
  busyRetries?: number
  /** `handed`: how the daemon put it in (`keys` = no mod confirmed it). */
  mode?: 'fill' | 'keys'
  /** When the daemon handed it over. */
  handedAt?: number
  /** Claude Code's own queue line (header.queued) renders this text right now: the entry is hidden, the line stands for it. */
  matched?: boolean
}

export class SendQueue {
  private list: QueueEntry[] = []
  private view: readonly QueueEntry[] = []
  private timer: ReturnType<typeof setTimeout> | null = null
  private claimed = new Set<string>()
  private disposed = false
  /** At most one: only the head can be waiting to retry a busy refusal. */
  private retry: { id: string; timer: ReturnType<typeof setTimeout> } | null = null
  /** Per `handed` entry: the timer that turns it `maybe` when nothing showed it. */
  private seen = new Map<string, ReturnType<typeof setTimeout>>()
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

  private clearRetry(): void {
    if (this.retry) clearTimeout(this.retry.timer)
    this.retry = null
  }

  private clearSeen(): void {
    for (const t of this.seen.values()) clearTimeout(t)
    this.seen.clear()
  }

  private emit(): void {
    // timers belong to entries still in the list in the state they wait in: any way out (echo, dismiss, resend) takes the timer too
    if (this.retry && !this.list.some((e) => e.id === this.retry!.id && e.state === 'sending')) this.clearRetry()
    for (const [id, t] of [...this.seen]) {
      if (!this.list.some((e) => e.id === id && e.state === 'sent')) { clearTimeout(t); this.seen.delete(id) }
    }
    this.view = this.list.map((e) => ({ ...e }))
    this.listeners.forEach((l) => l())
  }

  /** Let the held box go (the message left the App's hands, or was dropped): its previews are revoked, nothing of it comes back. */
  private release(e: QueueEntry): void {
    if (!e.held) return
    for (const a of e.held.chips) revokePreviewUrl(a.previewUrl)
    e.held = undefined
  }

  /** Queue a message (already planned / normalized). `held` is the box it came from, for a recall. Returns its client_msg_id. */
  enqueue(text: string, held?: HeldDraft): string {
    const at = this.now()
    const id = newClientMsgId()
    this.list.push({ id, ids: [id], text, state: 'undo', createdAt: at, undoUntil: at + UNDO_MS, startedAt: at, ...(held ? { held } : {}) })
    this.emit()
    this.pump()
    return id
  }

  /**
   * Take messages back from the App: only entries still in its hands (the send buffer, `undo`) can go; one the driver has handed out
   * (`sending` and later) cannot. The check and the removal are one synchronous step, so a message is either taken here or handed
   * out by `pump`, never both. Returns what the box held, in sending order.
   */
  private take(match: (e: QueueEntry) => boolean): Array<{ text: string; held?: HeldDraft }> {
    const taken = this.list.filter((e) => e.state === 'undo' && match(e))
    if (taken.length === 0) return []
    this.list = this.list.filter((e) => !taken.includes(e))
    this.emit()
    this.pump()
    return taken.map((e) => ({ text: e.text, ...(e.held ? { held: e.held } : {}) })) // the previews now belong to the caller
  }

  /** Take one message back (the Undo button, inside the send buffer): its box, or undefined when it has left. */
  takeBack(id: string): { text: string; held?: HeldDraft } | undefined { return this.take((e) => e.id === id)[0] }

  /** Take every message the App still holds back (the Up key in an empty box), oldest first. */
  takeAllHeld(): Array<{ text: string; held?: HeldDraft }> { return this.take(() => true) }

  /** How many messages the App still holds (the send buffer). */
  heldCount(): number { return this.list.filter((e) => e.state === 'undo').length }

  /** Take a message back inside the send buffer: returns its text for the box. */
  undo(id: string): string | undefined {
    const back = this.takeBack(id)
    if (back?.held) for (const a of back.held.chips) revokePreviewUrl(a.previewUrl) // a caller that only wants the text lets the previews go
    return back?.text
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
    this.list.splice(i + 1, 0, { id: next, ids: [next], text: e.text, state: 'undo', createdAt: at, undoUntil: at, startedAt: at })
    this.emit()
    this.pump()
    return next
  }

  dismiss(id: string): void {
    for (const x of this.list) if (x.id === id && x.state !== 'sending') this.release(x)
    this.list = this.list.filter((x) => x.id !== id || x.state === 'sending')
    this.emit()
  }

  /**
   * An observation of Claude Code's own queue line (header.queued, only the lines the deck actually renders: `shownHeaderQueue`, the `user`
   * ones): a `handed` entry whose text (trailing whitespace trimmed) is on a rendered line is `matched` (hidden: the line stands for it), one
   * to one in order; when the line stops being rendered without an echo it shows again, and past `HANDED_SEEN_MS` it is `maybe`. Idempotent:
   * it emits only on a change (the caller reports on every header change and on every queue change).
   */
  setHeaderQueue(userTexts: readonly string[]): void {
    const rest = userTexts.map((x) => x.trimEnd())
    let changed = false
    for (const e of this.list) {
      if (e.state !== 'sent' || e.mode === undefined) { if (e.matched) { e.matched = false; changed = true } continue }
      const i = rest.indexOf(e.text.trimEnd())
      const hit = i >= 0
      if (hit) rest.splice(i, 1)
      if (hit !== (e.matched === true)) { e.matched = hit; changed = true }
      if (!hit && this.staleHanded(e)) { this.markUnseen(e); changed = true }
    }
    if (changed) this.emit()
  }

  private staleHanded(e: QueueEntry): boolean {
    return e.state === 'sent' && e.handedAt !== undefined && !e.matched && this.now() - e.handedAt >= HANDED_SEEN_MS
  }

  private markUnseen(e: QueueEntry): void {
    e.state = 'maybe'
    e.outcome = { kind: 'unknown', reason: 'not_seen' }
  }

  /** The transcript's user messages: an entry whose echo is there is done (the transcript shows it from now on). */
  reconcile(items: readonly UserItem[]): void {
    const sources = this.port.echo?.sources ?? ['user']
    const users = items.filter((i) => sources.includes(i.source) && !this.claimed.has(i.id))
    const open = this.list.filter((e) => e.state === 'sent' || e.state === 'maybe' || e.state === 'sending')
    const done = new Set<QueueEntry>()
    const take = (e: QueueEntry, u: UserItem) => { this.claimed.add(u.id); done.add(e) }
    // 1. an item that carries a client_msg_id is paired by that id and by nothing else (the daemon already paired it)
    for (const u of users) {
      const e = u.client_msg_id !== undefined ? open.find((x) => x.ids.includes(u.client_msg_id!) && !done.has(x)) : undefined
      if (e) take(e, u)
    }
    // 2. the rest by text, in the daemon's window (2 s before the request, 30 s after it was handed out), nearest first.
    // A message still `sending` is never settled this way: only its own id or its transport outcome can.
    const pairs: Array<{ e: QueueEntry; u: UserItem; d: number }> = []
    for (const e of open) {
      if (e.state === 'sending' || done.has(e)) continue
      const to = e.state === 'sent' ? (e.settledAt ?? e.startedAt) + (this.port.echo?.afterMs ?? ECHO_BEFORE_MS) : e.startedAt + HAND_TIMEOUT_MS + ECHO_AFTER_MS
      for (const u of users) {
        if (this.claimed.has(u.id) || u.client_msg_id !== undefined) continue
        if (u.at < e.startedAt - ECHO_BEFORE_MS || u.at > to || normalizePrompt(u.text) !== e.text) continue
        pairs.push({ e, u, d: Math.abs(u.at - e.startedAt) })
      }
    }
    pairs.sort((a, b) => a.d - b.d)
    for (const p of pairs) if (!done.has(p.e) && !this.claimed.has(p.u.id)) take(p.e, p.u)
    if (done.size === 0) return
    for (const x of done) this.release(x)
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
    // an entry waiting to retry a busy refusal has no request in flight: it goes with the rest
    const waiting = this.retry?.id
    for (const e of this.list) if (e.state !== 'sending' || e.id === waiting) this.release(e)
    this.list = this.list.filter((e) => e.state === 'sending' && e.id !== waiting)
    this.clearRetry()
    this.clearSeen()
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
    this.clearRetry()
    this.clearSeen()
    for (const e of this.list) this.release(e)
    this.list = []
    this.view = []
    if (this.timer) clearTimeout(this.timer)
    this.timer = null
    this.listeners.clear()
  }

  // One at a time: the first entry that has not settled is the head.
  private pump(): void {
    if (this.disposed || this.retiredDone) return
    if (this.timer) { clearTimeout(this.timer); this.timer = null }
    const head = this.list.find((e) => e.state === 'undo' || e.state === 'sending')
    if (!head || head.state !== 'undo') return
    const left = head.undoUntil - this.now()
    if (left > 0) { this.timer = setTimeout(() => this.pump(), left); return }
    this.dispatch(head)
  }

  /** One attempt under the entry's current id. */
  private dispatch(e: QueueEntry): void {
    e.state = 'sending'
    e.startedAt = this.now()
    this.emit()
    void this.port.submit(e.text, e.ids[e.ids.length - 1]).then((o) => this.settle(e, o))
  }

  /** The retry timer fired: the next attempt, under the id chosen when it was scheduled. */
  private retryBusy(e: QueueEntry): void {
    this.retry = null
    if (this.disposed || this.retiredDone || e.state !== 'sending' || !this.list.includes(e)) return
    this.dispatch(e)
  }

  private settle(e: QueueEntry, o: SendOutcome): void {
    if (this.disposed) return
    if (this.retiredDone) {
      // retired: the answer changes nothing and starts nothing; the last one in flight frees the queue
      this.release(e)
      this.list = this.list.filter((x) => x !== e)
      if (this.list.every((x) => x.state !== 'sending')) { const done = this.retiredDone; this.dispose(); done() }
      return
    }
    if (e.state !== 'sending' || !this.list.includes(e)) return // already echoed by the transcript
    e.outcome = o
    e.settledAt = this.now()
    if (o.kind === 'refused' && o.reason === 'busy' && (e.busyRetries ?? 0) < BUSY_RETRIES) {
      // nothing was sent: try again, under a NEW id (a fill refusal is recorded under its id and would replay itself). The entry stays `sending`.
      const delay = BUSY_RETRY_BASE_MS * 2 ** (e.busyRetries ?? 0)
      e.busyRetries = (e.busyRetries ?? 0) + 1
      const next = newClientMsgId()
      e.ids.push(next)
      this.clearRetry()
      this.retry = { id: e.id, timer: setTimeout(() => this.retryBusy(e), delay) }
      this.emit()
      return
    }
    this.release(e) // the daemon took it (or it failed): its box is not coming back
    if (o.kind === 'accepted') e.state = 'sent'
    else if (o.kind === 'handed') {
      e.state = 'sent'
      e.mode = o.mode
      e.handedAt = this.now()
      this.seen.set(e.id, setTimeout(() => this.handedTimeout(e), HANDED_SEEN_MS))
    }
    else if (mayHaveRun(o)) e.state = 'maybe'
    else e.state = 'failed'
    this.emit()
    this.pump()
  }

  /** `handed` and nothing showed it for `HANDED_SEEN_MS`: neither the queue line nor the transcript. */
  private handedTimeout(e: QueueEntry): void {
    this.seen.delete(e.id)
    if (this.disposed || !this.list.includes(e) || !this.staleHanded(e)) return
    this.markUnseen(e)
    this.emit()
  }
}

const queues = new Map<string, SendQueue>()

/** The pane's queue: it survives the component (a tab switch unmounts the pane). Made on the port of the adapter bound to `key` (a worker's), else on `make` (a terminal's). */
export function sendQueueFor(key: string, make: () => SendPort): SendQueue {
  let q = queues.get(key)
  if (!q) { q = new SendQueue((adapterOf(key)?.port ?? make)()); queues.set(key, q) }
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

// A retired queue's request in flight is still a write on its way to the execution: the pane's hand-over controls wait for it (#2659).
// Counted per key and announced, because a retired queue emits nothing of its own.
const retiringByKey = new Map<string, number>()
const retiringListeners = new Set<() => void>()
const announceRetiring = (): void => retiringListeners.forEach((l) => l())
export const subscribeRetiring = (fn: () => void): (() => void) => { retiringListeners.add(fn); return () => { retiringListeners.delete(fn) } }
/** How many retired queues whose key starts with `prefix` still have a request in flight. */
export function retiringInFlight(prefix: string): number {
  let n = 0
  for (const [key, c] of retiringByKey) if (key.startsWith(prefix)) n += c
  return n
}

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
    retiringByKey.set(key, (retiringByKey.get(key) ?? 0) + 1)
    q.retire(() => {
      retiring--
      const left = (retiringByKey.get(key) ?? 1) - 1
      if (left > 0) retiringByKey.set(key, left); else retiringByKey.delete(key)
      announceRetiring()
    })
    announceRetiring()
  }
}

/** Tests only. */
export function clearAllSendQueues(): void {
  retiring = 0
  retiringByKey.clear()
  queues.forEach((q) => q.dispose())
  queues.clear()
}
