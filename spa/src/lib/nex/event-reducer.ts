// spa/src/lib/nex/event-reducer.ts — pure reducer from Nexen durable events
// to the per-execution view state (spec §4.2.4), plus the partial assembly
// fed by transient stream frames (P-B2 spec §4.1). No React, no fetch, no
// store: the hook feeds it history pages and SSE frames alike.
import type { StreamMessage } from './message-types'
import { finalizeBlock, type PartialAssembly } from './partial'
import type { NexSseFrame } from './sse-parser'
import { endTurn, recordToolEnds, recordToolStarts, type ToolActivity } from './tool-activity'
import type { ExecutionSummary, NexEvent } from './types'

export type { PartialAssembly, PartialBlock } from './partial'
export { applyTransientFrame, finalizedFor } from './partial'
export type { ToolActivity } from './tool-activity'

export interface ExecutionState {
  summary: ExecutionSummary | null
  /** Provider passthrough + synthetic user bubbles, in seq order. */
  messages: StreamMessage[]
  /** Highest durable seq applied (history or SSE). Never moved by transient frames. */
  lastSeq: number
  historyLoaded: boolean
  /** A lifecycle event arrived; the summary is authoritative, so the hook refetches. */
  summaryStale: boolean
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
  pendingLocal: { text: string; delivery: 'delivered' | 'queued' | null } | null
  sendError: { code: string; message: string; turnId?: string } | null
  lastTurn: { turnId: string; delivery: 'delivered' | 'queued' } | null
  /** In-flight assistant message from transient frames; only applyTransientFrame / D-rules write it. */
  partial: PartialAssembly | null
  /** A turn is running per the event stream (observers see it too, unlike pendingSend). */
  turnLive: boolean
  /** Keyed by tool_use id; written by the durable A-rules only. */
  tools: Record<string, ToolActivity>
}

export function defaultExecutionState(): ExecutionState {
  return {
    summary: null,
    messages: [],
    lastSeq: 0,
    historyLoaded: false,
    summaryStale: false,
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
    tools: {},
  }
}

/** Nexen's own (closed-set) kinds; everything else is provider passthrough. */
export function isLifecycleKind(kind: string): boolean {
  return kind.startsWith('execution.') || kind.startsWith('lease.')
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

function userBubble(text: string): StreamMessage {
  return { type: 'user', message: { role: 'user', content: [{ type: 'text', text }], stop_reason: null } } as StreamMessage
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

/** Spec §4.1 D1–D4 and §4.2 A1–A4; runs after the seq guard, before the per-kind reducers. */
function applyTurnRules(s: ExecutionState, ev: NexEvent, p: Record<string, unknown>): ExecutionState {
  // A subagent's frames (non-null parent_tool_use_id) — including its own
  // `result` — must never end the main turn or touch the main partial/tools.
  if (!isLifecycleKind(ev.kind) && p.parent_tool_use_id != null) return s
  if (TURN_ENDING_KINDS.has(ev.kind)) return endTurn(s, ev.created_at)
  if (ev.kind === 'execution.running' || ev.kind === 'execution.message_accepted') return { ...s, turnLive: true }
  if (ev.kind === 'assistant') return finalizeBlock(recordToolStarts(s, p, ev.created_at), p)
  if (ev.kind === 'user') return recordToolEnds(s, p, ev.created_at)
  return s
}

export function applyDurableEvent(s: ExecutionState, ev: NexEvent): ExecutionState {
  if (!Number.isFinite(ev.seq) || ev.seq <= s.lastSeq) return s
  const p = ev.payload ?? {}
  let next: ExecutionState = applyTurnRules({ ...s, lastSeq: ev.seq }, ev, p)

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
      if (brief !== undefined) next = { ...next, messages: [...next.messages, userBubble(brief)] }
      return patchSummary(next, {})
    }
    case 'execution.message_accepted': {
      // Also a lifecycle event: turn_count / live_turn_id / event_count on the
      // summary moved, so the hook refetches (summaryStale) like any other.
      // Same empty-vs-absent distinction as execution.delegated above.
      const text = str(p, 'text')
      next = { ...next, pendingLocal: null, summaryStale: true }
      if (text !== undefined) next = { ...next, messages: [...next.messages, userBubble(text)] }
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
