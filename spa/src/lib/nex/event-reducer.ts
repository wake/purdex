// spa/src/lib/nex/event-reducer.ts — pure reducer from Nexen durable events
// to the per-execution view state (spec §4.2.4), plus the partial assembly
// fed by transient stream frames (P-B2 spec §4.1) and the N2 tool overlay
// from the derived `tool_use` / `tool_result` kinds (P-B3 spec §4.3). No
// React, no fetch, no store: the hook feeds it history pages and SSE frames
// alike.
import { parseAttachmentMeta, type AttachmentMeta } from './attachments'
import { isResultError } from './cost-summary'
import type { StreamMessage } from './message-types'
import { defaultPreludeState, type PreludeState } from './prelude'
import { finalizeBlock, type PartialAssembly } from './partial'
import type { NexSseFrame } from './sse-parser'
import { endTurn, recordN2ToolResult, recordN2ToolUse, recordToolEnds, recordToolStarts, type ToolActivity } from './tool-activity'
import { applyTaskEvent, applyTaskSnapshot, type TaskTable } from './tasks'
import type { ExecutionSummary, NexEvent, WorkerTasksSnapshot } from './types'

export type { PartialAssembly, PartialBlock } from './partial'
export { applyTransientFrame, finalizedFor } from './partial'
export type { ToolActivity } from './tool-activity'

export type TurnOutcome = 'ok' | 'failed' | 'interrupted'

export interface TurnMeta {
  /** created_at of the event that opened the turn (0 when the frame carried none). */
  startAt: number
  /** created_at of the first main-turn end event; null while the turn is live. */
  endAt: number | null
  outcome: TurnOutcome | null
  /** result.duration_ms, else endAt − startAt when both are > 0, else null. */
  durationMs: number | null
}

/**
 * Reducer-internal bookkeeping for one turn, index-aligned with `turnMeta`.
 * `state`: `null` nothing ended it yet; `'soft'` a `result` or an
 * execution-scoped end (terminate / archive) stamped it and a turn-scoped
 * lifecycle end may still refine the outcome; `'sealed'` a turn-scoped
 * lifecycle end closed it. `bySource` records that an
 * `execution.interrupted` already decided the outcome from its `source`.
 * `resulted`: a top-level `result` stamped it, so `durationMs` is the
 * result's (or the fallback when it carried none) and no later event may
 * replace it.
 */
export interface TurnEnd {
  /** The daemon's turn_id; null for turn 1 (execution.delegated carries none) until a keyed end binds it. */
  turnId: string | null
  state: null | 'soft' | 'sealed'
  bySource: boolean
  resulted: boolean
}

export interface ExecutionState {
  summary: ExecutionSummary | null
  /** Provider passthrough + synthetic user bubbles, in seq order. */
  messages: StreamMessage[]
  /** Highest durable seq applied (history or SSE). Never moved by transient frames. */
  lastSeq: number
  /**
   * Newest `created_at` among the applied durable events (max-monotonic; 0 =
   * none). Task events are excluded like they are from `lastSeq`: they bypass
   * the seq guard and may replay. The projection compares it with a list
   * row's `updated_at` to tell which of the two is fresher.
   */
  lastEventAt: number
  historyLoaded: boolean
  /** A lifecycle event arrived; the summary is authoritative, so the hook refetches. */
  summaryStale: boolean
  /** Bumped by a local summary patch (exit result); a summary fetch that started earlier is dropped. */
  summaryGen: number
  /**
   * 'paused' (spec §4.3.2 step 4) is the store-only state P-B.2's
   * subscription-slot cap sets when this execution's SSE is deliberately
   * not connected (another pane holds the slot) — never emitted by
   * NexSseStatus, which openNexSse alone owns.
   */
  sse: 'idle' | 'connecting' | 'open' | 'reconnecting' | 'closed' | 'paused'
  sseError: string | null
  /** The lease THIS tab holds — written only from attach(control)/renew responses (I11). */
  lease: { leaseId: string; expiresAt: number } | null
  leaseError: { code: string; heldBy?: string } | null
  pendingSend: boolean
  /** Optimistic user bubble; replaced by the durable execution.message_accepted. */
  pendingLocal: {
    text: string
    delivery: 'delivered' | 'queued' | null
    /**
     * Thumbnails of the native images this send carries (phase E). The
     * object URLs belong to the optimistic line — the execution store
     * revokes them on the write that drops this array (revokeDroppedPreviews),
     * never a pane — and never to the chips they were sent from.
     */
    attachments?: { previewUrl: string; media_type: string }[]
  } | null
  /** `attachmentIndex`: the offending image on Nexen's per-image attachment errors (contract §1.9). */
  sendError: { code: string; message: string; turnId?: string; attachmentIndex?: number } | null
  lastTurn: { turnId: string; delivery: 'delivered' | 'queued' } | null
  /** In-flight assistant message from transient frames; only applyTransientFrame / D-rules write it. */
  partial: PartialAssembly | null
  /** A turn is running per the event stream (observers see it too, unlike pendingSend). */
  turnLive: boolean
  /**
   * Index into `messages` where each turn begins, in ascending order
   * (spec §4.1). Written when the daemon says a turn opened
   * (execution.message_accepted / execution.delegated), **before** the
   * bubble those events may or may not append — so the boundary is exact
   * even when the payload carried no text.
   */
  turnStarts: number[]
  /**
   * Timing and outcome per turn, index-aligned with `turnStarts` (worker-pane
   * theme spec §7.1). Pushed by the same events that push a boundary, stamped
   * by the main turn's end events; both come from durable events, so replay
   * rebuilds it exactly.
   */
  turnMeta: TurnMeta[]
  /** Reducer bookkeeping per turn, index-aligned with `turnMeta` (see TurnEnd). */
  turnEnds: TurnEnd[]
  /**
   * Keyed by tool_use id; written only by the durable A-rules (raw
   * assistant / user frames) and the N-rules (derived tool_use / tool_result
   * events, which overlay the daemon's facts onto the same entry).
   */
  tools: Record<string, ToolActivity>
  /**
   * Background tasks (Bash `run_in_background` / subagents), keyed by
   * task_id. Written only by the durable `task_start` / `task_end` kinds
   * (nexen v0.13) and by `applyTasksSnapshot`; always `{}` on an older daemon.
   */
  tasks: TaskTable
  /** The transcript before turn 1 (worker prelude spec §5.2). Its own slice: never read by the rules above. */
  prelude: PreludeState
}

export function defaultExecutionState(): ExecutionState {
  return {
    summary: null,
    messages: [],
    lastSeq: 0,
    lastEventAt: 0,
    historyLoaded: false,
    summaryStale: false,
    summaryGen: 0,
    sse: 'idle',
    sseError: null,
    lease: null,
    leaseError: null,
    pendingSend: false,
    pendingLocal: null,
    sendError: null,
    lastTurn: null,
    partial: null,
    turnLive: false,
    turnStarts: [],
    turnMeta: [],
    turnEnds: [],
    tools: {},
    tasks: {},
    prelude: defaultPreludeState(),
  }
}

/**
 * A turn the daemon accepted has not ended yet — no main-turn end event has
 * stamped it (its outcome is still null). Unlike the execution-wide
 * `turnLive`, which the first turn's end clears, this stays true while a
 * queued send (accepted during the previous turn) still waits or runs
 * (worker-pane theme spec §8.2: running from the accepted send until that
 * turn ends).
 */
export function hasOpenTurn(s: ExecutionState): boolean {
  return s.turnMeta.some((m) => m.outcome === null)
}

/** The outcome of the most recent turn that has ended; null when none has. */
export function lastEndedOutcome(s: ExecutionState): TurnOutcome | null {
  for (let i = s.turnMeta.length - 1; i >= 0; i--) {
    const o = s.turnMeta[i].outcome
    if (o !== null) return o
  }
  return null
}

/** Nexen's own (closed-set) kinds; everything else is provider passthrough. */
export function isLifecycleKind(kind: string): boolean {
  return kind.startsWith('execution.') || kind.startsWith('lease.')
}

/**
 * Nexen's derived tool kinds (v0.12.0, `capabilities.tool_events`). They are
 * neither lifecycle nor messages: the N-rules fold them into `tools` and they
 * are never appended to `messages` (P-B3 spec §4.1 / §4.6).
 */
export function isToolEventKind(kind: string): boolean {
  return kind === 'tool_use' || kind === 'tool_result'
}

/**
 * Nexen's background-task kinds (v0.13, `capabilities.worker_rollup`). Like
 * the tool kinds they are not provider passthrough and not lifecycle: they
 * only update `tasks` — never a message, never a turn boundary, never the
 * partial.
 */
export function isTaskEventKind(kind: string): boolean {
  return kind === 'task_start' || kind === 'task_end'
}

/**
 * Merge a `GET /v1/executions/{id}/tasks` snapshot into the task table (the
 * #83 correction after every SSE (re)open). Not an event: `lastSeq` and
 * everything else stay as they are. Merge rules in `tasks.ts`.
 */
export function applyTasksSnapshot(s: ExecutionState, snapshot: WorkerTasksSnapshot): ExecutionState {
  const tasks = applyTaskSnapshot(s.tasks, snapshot.items, snapshot.cursor)
  return { ...s, tasks }
}

/**
 * Durable SSE frame → NexEvent. Returns null for transient frames (no id)
 * and for data that is not JSON. Accepts both the full eventView shape and
 * a bare payload (then seq comes from the id line and kind from event:).
 *
 * The SSE `id:` line is always authoritative for seq — never the wrapper's
 * own `seq` field. A wrapper is only trusted (its kind/payload/execution_id/
 * created_at adopted) when its `seq` agrees with `id:` or is absent; a
 * disagreeing `seq` is a sign the "wrapper" is untrusted/malformed data, not
 * a real eventView, so the whole object is instead treated as a bare
 * payload. Trusting the wrapper's seq unconditionally would let a single
 * bad frame set lastSeq far ahead and silently drop every real event up to
 * it as a duplicate.
 */
export function frameToEvent(frame: NexSseFrame): NexEvent | null {
  if (frame.id == null) return null
  let parsed: unknown
  try {
    parsed = JSON.parse(frame.data)
  } catch {
    return null
  }
  if (!parsed || typeof parsed !== 'object') return null
  const obj = parsed as Record<string, unknown>
  const idSeq = Number(frame.id)
  const wrapperSeqOk = obj.seq === undefined || obj.seq === idSeq
  if (wrapperSeqOk && typeof obj.kind === 'string' && obj.payload && typeof obj.payload === 'object') {
    return {
      seq: idSeq,
      execution_id: typeof obj.execution_id === 'string' ? obj.execution_id : '',
      kind: obj.kind,
      payload: obj.payload as Record<string, unknown>,
      created_at: typeof obj.created_at === 'number' ? obj.created_at : 0,
    }
  }
  return { seq: idSeq, execution_id: '', kind: frame.event, payload: obj, created_at: 0 }
}

/**
 * A synthetic user message. Image attachments (phase E) ride as the side
 * field `purdex_attachments`, present only when there are some — a message
 * without images is byte-for-byte the phase D shape.
 */
function userBubble(text: string, attachments: AttachmentMeta[] = []): StreamMessage {
  const msg = { type: 'user', message: { role: 'user', content: [{ type: 'text', text }], stop_reason: null } }
  return (attachments.length > 0 ? { ...msg, purdex_attachments: attachments } : msg) as StreamMessage
}

/**
 * The bubble a turn-opening event appends, or null for none. Text absent is
 * the site-wide stream's stripping (spec §4.2.4) — no bubble, unless the
 * event still carries images (the site-wide stream strips those too, so this
 * only guards a payload shape that should not occur). An empty text with
 * images is an image-only message and gets its line (Review Focus 4).
 */
function turnBubble(text: string | undefined, rawAttachments: unknown): StreamMessage | null {
  const attachments = parseAttachmentMeta(rawAttachments)
  if (text === undefined && attachments.length === 0) return null
  return userBubble(text ?? '', attachments)
}

/**
 * The daemon said a turn opened: record where it begins, before the bubble
 * the event may or may not append. **Repeated indexes are kept.** Two
 * boundaries at one index mean the first turn appended nothing — a payload
 * with no text, which is the very case this field exists for (spec §4.1) —
 * and collapsing them would merge two turns the daemon declared separately.
 * The seq guard in `applyDurableEvent` already makes applying one event twice
 * impossible, so a dedupe here would protect nothing. Consumers therefore
 * have to tolerate an empty turn range (`start === end`).
 */
function markTurnStart(s: ExecutionState, createdAt: number, turnId: string | null): ExecutionState {
  return {
    ...s,
    turnStarts: [...s.turnStarts, s.messages.length],
    turnMeta: [...s.turnMeta, { startAt: createdAt, endAt: null, outcome: null, durationMs: null }],
    turnEnds: [...s.turnEnds, { turnId, state: null, bySource: false, resulted: false }],
  }
}

/**
 * `execution.terminal`'s `reason` is the turn's `last_turn_reason` (nexen
 * v0.13.2 docs/contract/capability-matrix.md, "turn 終局原因"):
 *
 * | reason              | outcome                                       |
 * |---------------------|-----------------------------------------------|
 * | `final_response`    | normal end: keep a result's outcome, else ok  |
 * | `interrupted`       | interrupted; the execution.interrupted that   |
 * |                     | follows refines it by source (see below)      |
 * | `error`             | failed                                        |
 * | `session_expired`   | failed (the turn could not start)             |
 * | `orphaned`          | failed (daemon restart reconciled the turn)   |
 * | `auth_failed`       | failed                                        |
 * | `permission_denied` | failed (reserved, no producer yet)            |
 * | `context_exhausted` | failed (reserved, no producer yet)            |
 * | `quota_exhausted`   | failed (reserved, no producer yet)            |
 * | anything else       | failed — a failure is never hidden (spec F3)  |
 *
 * `'normal'` means "keep what a result said, else ok".
 */
function terminalOutcome(reason: string | undefined): TurnOutcome | 'normal' {
  if (reason === 'final_response') return 'normal'
  if (reason === 'interrupted') return 'interrupted'
  return 'failed'
}

/**
 * `execution.interrupted`'s `source` (nexen v0.13.2 capability-matrix §3,
 * "打斷來源子表"; closed vocabulary `execution.InterruptSources`). Spec F3
 * hides only an interrupt a human asked for; every other source is a
 * failure the user must see (controller ruling, fix round 1):
 *
 * | source            | meaning                                   | outcome     |
 * |-------------------|-------------------------------------------|-------------|
 * | `user`            | the user pressed interrupt                | interrupted |
 * | `terminated`      | the interrupt a human `Terminate` carries | interrupted |
 * | (absent / empty)  | older payload without a source            | interrupted |
 * | `turn_timeout`    | the whole-turn watchdog fired             | failed      |
 * | `daemon_shutdown` | graceful shutdown interrupted live turns  | failed      |
 * | `quota`           | hit the quota (reserved, no producer yet) | failed      |
 * | anything else     | unknown — a failure is never hidden       | failed      |
 *
 * `execution.terminal` carries no source (execution/turn.go:610), so a
 * terminal{interrupted} reads interrupted until the execution.interrupted
 * nexen emits right after it, keyed by the same turn_id, decides.
 */
function interruptOutcome(source: string | undefined): TurnOutcome {
  if (!source || source === 'user' || source === 'terminated') return 'interrupted'
  return 'failed'
}

/** The outcome a main-turn end event proposes (spec §7.1). */
function endOutcome(kind: string, p: Record<string, unknown>): TurnOutcome | 'normal' {
  switch (kind) {
    case 'result':
      return isResultError(p) ? 'failed' : 'ok'
    case 'execution.terminal':
      return terminalOutcome(str(p, 'reason'))
    case 'execution.interrupted':
      return interruptOutcome(str(p, 'source'))
    case 'execution.terminated':
    case 'execution.archived':
      return 'interrupted'
    default:
      // execution.error / rejected / turn_stalled / turn_orphaned
      return 'failed'
  }
}

/** Execution-scoped ends: no turn_id, and they may land after a turn already finished. */
const EXECUTION_SCOPED_ENDS = new Set(['execution.terminated', 'execution.archived'])

/**
 * Which turn an end event belongs to. Nexen accepts a send while the
 * previous turn is still live (`message_accepted` is emitted at send time,
 * execution/service.go:782-803, capability-matrix §2 #22), so "the last
 * turn" is wrong once a send is queued. Queued turns run FIFO, hence:
 *
 * - `result` (no turn_id): the oldest turn not yet sealed.
 * - turn-scoped lifecycle ends (`terminal`, `interrupted`, `error`,
 *   `turn_stalled`, `turn_orphaned`, `rejected`): the turn with that
 *   `turn_id`, sealed or not; unknown / missing turn_id → the oldest
 *   unsealed turn (binding the id when that turn has none, i.e. turn 1 from
 *   `execution.delegated`).
 * - `terminated` / `archived`: the oldest unsealed turn too, but they only
 *   stamp it when it has no outcome yet (stampTurnEnd never lets them
 *   refine) — so they never re-mark a turn whose result already arrived,
 *   and never reach past it to a queued turn (that one gets its own keyed
 *   `turn_stalled`).
 */
function findEndTarget(s: ExecutionState, ev: NexEvent, p: Record<string, unknown>): { i: number; bind: string | null } | null {
  const ends = s.turnEnds
  if (ev.kind === 'result' || EXECUTION_SCOPED_ENDS.has(ev.kind)) {
    const i = ends.findIndex(e => e.state !== 'sealed')
    return i < 0 ? null : { i, bind: null }
  }
  const turnId = str(p, 'turn_id') || null
  if (turnId) {
    const exact = ends.findIndex(e => e.turnId === turnId)
    if (exact >= 0) return { i: exact, bind: null }
  }
  const i = ends.findIndex(e => e.state !== 'sealed')
  if (i < 0) return null
  return { i, bind: ends[i].turnId === null ? turnId : null }
}

/**
 * Stamp a turn's meta at a main-turn end (spec §7.1). The outcome is written
 * once, except that a turn-scoped lifecycle end may refine it: `interrupted`
 * wins (CC emits a result after an interrupt) and `failed` beats `ok` (a
 * failure is never hidden, F3). An `execution.interrupted` is authoritative:
 * its source decides, and nothing after it changes the outcome. `endAt` is
 * filled once. `durationMs` is filled once too, except that a result's
 * finite `duration_ms` replaces an `endAt − startAt` fallback an
 * execution-scoped end left on a still-unsealed turn (§7.1:
 * result.duration_ms wins, R1-1) — a value a result set is never replaced.
 * A result never reaches a sealed turn: nexen v0.13.2 persists the provider
 * result before concludeTurn emits the sealing terminal / interrupted, and
 * interrupted / error paths emit none (execution/turn.go:282-409).
 */
function stampTurnEnd(s: ExecutionState, ev: NexEvent, p: Record<string, unknown>): ExecutionState {
  const target = findEndTarget(s, ev, p)
  if (!target) return s
  const { i } = target
  const end = s.turnEnds[i]
  const meta = s.turnMeta[i]
  const isResult = ev.kind === 'result'
  const execScoped = EXECUTION_SCOPED_ENDS.has(ev.kind)
  const isInterrupt = ev.kind === 'execution.interrupted'
  const proposed = endOutcome(ev.kind, p)

  let outcome = meta.outcome
  if (isInterrupt) outcome = proposed === 'normal' ? outcome : proposed
  else if (end.bySource) { /* the interrupt's source already decided */ }
  else if (proposed === 'normal') outcome = outcome ?? 'ok'
  else if (outcome === null) outcome = proposed
  else if (!isResult && !execScoped && (proposed === 'interrupted' || (proposed === 'failed' && outcome === 'ok'))) outcome = proposed

  const endAt = meta.endAt ?? (ev.created_at > 0 ? ev.created_at : null)
  let durationMs = meta.durationMs
  if (isResult && !end.resulted && typeof p.duration_ms === 'number' && Number.isFinite(p.duration_ms) && p.duration_ms >= 0) {
    durationMs = p.duration_ms
  }
  if (durationMs === null && endAt !== null && meta.startAt > 0 && endAt >= meta.startAt) durationMs = endAt - meta.startAt

  const turnMeta = s.turnMeta.slice()
  turnMeta[i] = { startAt: meta.startAt, endAt, outcome, durationMs }
  const turnEnds = s.turnEnds.slice()
  turnEnds[i] = {
    turnId: end.turnId ?? target.bind,
    state: end.state === 'sealed' || (!isResult && !execScoped) ? 'sealed' : 'soft',
    bySource: end.bySource || isInterrupt,
    resulted: end.resulted || isResult,
  }
  return { ...s, turnMeta, turnEnds }
}

function str(p: Record<string, unknown>, k: string): string | undefined {
  const v = p[k]
  return typeof v === 'string' ? v : undefined
}

function patchSummary(s: ExecutionState, patch: Partial<ExecutionSummary>): ExecutionState {
  return { ...s, summaryStale: true, summary: s.summary ? { ...s.summary, ...patch } : null }
}

/** D3 + D4: the turn is over, so nothing can still be streaming or running. */
const TURN_ENDING_KINDS = new Set([
  'result', 'execution.terminal', 'execution.error', 'execution.rejected', 'execution.terminated',
  'execution.interrupted', 'execution.turn_orphaned', 'execution.turn_stalled', 'execution.archived',
])

/**
 * P-B2 spec §4.1 D1–D4, §4.2 A1–A4 and P-B3 spec §4.3 N0–N2; runs after the
 * seq guard, before the per-kind reducers.
 */
function applyTurnRules(s: ExecutionState, ev: NexEvent, p: Record<string, unknown>): ExecutionState {
  // N1 / N2: the derived kinds only overlay `tools`; they are not in
  // TURN_ENDING_KINDS, so they are safe for a subagent's events too.
  if (ev.kind === 'tool_use') return recordN2ToolUse(s, p, ev.created_at)
  if (ev.kind === 'tool_result') return recordN2ToolResult(s, p, ev.created_at)
  // A subagent's frames (non-null parent_tool_use_id) — including its own
  // `result` — must never end the main turn, set turnLive or touch the main
  // partial. Its tool timing is still recorded (#1228): tool_use ids are
  // globally unique, so the one `tools` map is the right home, and endTurn
  // on the parent's turn end aborts whatever the child left running.
  if (!isLifecycleKind(ev.kind) && p.parent_tool_use_id != null) {
    if (ev.kind === 'assistant') return recordToolStarts(s, p, ev.created_at)
    if (ev.kind === 'user') return recordToolEnds(s, p, ev.created_at)
    return s
  }
  if (TURN_ENDING_KINDS.has(ev.kind)) return stampTurnEnd(endTurn(s, ev.created_at), ev, p)
  if (ev.kind === 'execution.running' || ev.kind === 'execution.message_accepted') return { ...s, turnLive: true }
  if (ev.kind === 'assistant') return finalizeBlock(recordToolStarts(s, p, ev.created_at), p)
  if (ev.kind === 'user') return recordToolEnds(s, p, ev.created_at)
  return s
}

export function applyDurableEvent(s: ExecutionState, ev: NexEvent): ExecutionState {
  if (!Number.isFinite(ev.seq)) return s
  const p = ev.payload ?? {}

  // Task kinds only touch `tasks`. Handled before applyTurnRules: a shell
  // started inside a subagent carries a non-null parent_tool_use_id, and
  // the subagent branch there would swallow it.
  // They bypass the seq guard: nexen's live SSE can deliver a regressing seq
  // (commit→publish is not globally serialised, consumer-guide #83), and
  // dropping a late task_end would leave the row running forever. Safe
  // because task events are idempotent per task_id and closure is final.
  // They also never touch `lastSeq`, neither raising nor lowering it: it is
  // the shared high-water mark every other kind is guarded by, and the SSE
  // Last-Event-ID on reconnect. A task event arriving early with a higher seq
  // must not make a later lower-seq assistant / user / result frame look
  // already-seen (dropped now, and skipped for good on reconnect). Cost: when
  // the last durable event is a task event, reconnect replays it once —
  // harmless, by the same idempotence.
  if (isTaskEventKind(ev.kind)) {
    const tasks = applyTaskEvent(s.tasks, ev.kind, p, ev.seq)
    return tasks === s.tasks ? s : { ...s, tasks }
  }
  if (ev.seq <= s.lastSeq) return s

  const lastEventAt = ev.created_at > s.lastEventAt ? ev.created_at : s.lastEventAt
  let next: ExecutionState = applyTurnRules({ ...s, lastSeq: ev.seq, lastEventAt }, ev, p)

  // The N2 tool kinds are consumed by applyTurnRules alone: not a message
  // (appending them would leave invisible entries behind), not a turn end,
  // not a send acknowledgement — pendingSend / turnLive / partial / summary
  // are untouched.
  if (isToolEventKind(ev.kind)) return next

  if (!isLifecycleKind(ev.kind)) {
    next = { ...next, messages: [...next.messages, p as StreamMessage] }
    if (ev.kind === 'result' && p.parent_tool_use_id == null) next = { ...next, pendingSend: false }
    return next
  }

  switch (ev.kind) {
    case 'execution.delegated': {
      // Nexen always emits `brief` on execution-scoped streams; an empty
      // string means "the human said nothing" and still gets a (empty)
      // bubble, while an absent key means the site-wide stream stripped it
      // (spec §4.2.4). A truthy check would conflate the two.
      const brief = str(p, 'brief')
      // execution/service.go:551 — no turn_id here; the first keyed end binds it.
      next = markTurnStart(next, ev.created_at, null)
      const bubble = turnBubble(brief, p.attachments)
      if (bubble) next = { ...next, messages: [...next.messages, bubble] }
      return patchSummary(next, {})
    }
    case 'execution.message_accepted': {
      // Also a lifecycle event: turn_count / live_turn_id / event_count on the
      // summary moved, so the hook refetches (summaryStale) like any other.
      // Same empty-vs-absent distinction as execution.delegated above.
      const text = str(p, 'text')
      next = markTurnStart({ ...next, pendingLocal: null, summaryStale: true }, ev.created_at, str(p, 'turn_id') || null)
      const bubble = turnBubble(text, p.attachments)
      if (bubble) next = { ...next, messages: [...next.messages, bubble] }
      return next
    }
    case 'execution.running':
      return patchSummary(next, { state: 'running' })
    case 'execution.terminal': {
      // execution/turn.go:568 — {turn_id, reason, state, detail?}; state is
      // the execution's state after the turn ended (idle, or failed, or a
      // terminal state that outranked it), so take it rather than assume idle.
      next = { ...next, pendingSend: false }
      const reason = str(p, 'reason')
      const state = str(p, 'state') ?? 'idle'
      return patchSummary(next, { state, ...(reason ? { last_turn_reason: reason } : {}) })
    }
    case 'execution.error':
      return patchSummary({ ...next, pendingSend: false }, {})
    case 'execution.rejected':
      return patchSummary(next, { state: 'rejected', ...(str(p, 'reason') ? { reject_reason: str(p, 'reason') } : {}) })
    case 'execution.terminated':
      // execution/service.go:1184 — {principal_id} only; terminal_reason is
      // the summary's business, the refetch brings it.
      return patchSummary({ ...next, pendingSend: false }, { state: 'terminated' })
    case 'execution.archived':
      return patchSummary(next, { archived: true })
    case 'execution.unarchived':
      return patchSummary(next, { archived: false })
    case 'execution.observer_attached':
    case 'execution.observer_detached': {
      const observers = p.observers
      return patchSummary(next, typeof observers === 'number' ? { observers } : {})
    }
    case 'lease.acquired': {
      const principal_id = str(p, 'principal_id')
      const expires_at = typeof p.expires_at === 'number' ? p.expires_at : 0
      return patchSummary(next, principal_id ? { lease: { principal_id, expires_at } } : {})
    }
    case 'lease.released': {
      if (!next.summary) return { ...next, summaryStale: true }
      const { lease: _dropped, ...rest } = next.summary
      return { ...next, summaryStale: true, summary: rest as ExecutionSummary }
    }
    case 'execution.turn_orphaned':
      // A daemon restart reconciled a live turn with no execution.terminal:
      // the input must not stay locked forever, so clear pendingSend;
      // pendingLocal (the optimistic bubble) is left alone — the turn is
      // still live, just orphaned from this client's view.
      return { ...next, pendingSend: false, summaryStale: true }
    case 'execution.turn_stalled':
      // Same restart reconcile, but for a queued turn the daemon withdraws
      // outright: both the pending flag and the optimistic bubble must
      // clear, or the input stays locked and a bubble is stuck forever.
      return { ...next, pendingSend: false, pendingLocal: null, summaryStale: true }
    default:
      // interrupt_requested / interrupted …: nothing to render in P-B; the
      // summary refetch carries the state.
      return { ...next, summaryStale: true }
  }
}
