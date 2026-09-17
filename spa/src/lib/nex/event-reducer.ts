// spa/src/lib/nex/event-reducer.ts — pure reducer from Nexen durable events
// to the per-execution view state (spec §4.2.4), plus the partial assembly
// fed by transient stream frames (P-B2 spec §4.1). No React, no fetch, no
// store: the hook feeds it history pages and SSE frames alike.
import type { StreamMessage } from '../stream-ws'
import type { NexSseFrame } from './sse-parser'
import type { ExecutionSummary, NexEvent } from './types'

export interface PartialBlock {
  index: number
  type: 'text' | 'thinking' | 'tool_use' | 'unknown'
  text: string
  thinking: string
  /** Raw byte prefix of the tool input, never parsed. */
  partialJson: string
  toolId?: string
  toolName?: string
}

export interface PartialAssembly {
  messageId: string | null
  /**
   * Number of blocks of this message already finalized by durable
   * `assistant` frames (Nexen's `assembly.finalized`). Deltas / starts for
   * index < finalized are dropped; D1 finalizes exactly index `finalized`.
   */
  finalized: number
  /** Sparse: only unfinalized indices. */
  blocks: Record<number, PartialBlock>
}

export interface ToolActivity {
  name: string
  /** ev.created_at (unix ms, server clock); 0 = unknown, renderer shows no timer. */
  startedAt: number
  endedAt: number | null
  status: 'running' | 'done' | 'error' | 'aborted'
}

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

function contentBlocks(p: Record<string, unknown>): Record<string, unknown>[] {
  const content = obj(p.message)?.content
  return Array.isArray(content) ? content.map(obj).filter((b): b is Record<string, unknown> => b !== null) : []
}

function endTurn(s: ExecutionState, at: number): ExecutionState {
  const tools = { ...s.tools }
  for (const [id, t] of Object.entries(tools)) {
    if (t.status === 'running') tools[id] = { ...t, endedAt: at, status: 'aborted' }
  }
  return { ...s, partial: null, turnLive: false, tools }
}

function finalizeBlock(s: ExecutionState, p: Record<string, unknown>): ExecutionState {
  const partial = s.partial
  if (!partial) return s
  // Only a PRESENT id that disagrees supersedes the assembly; a frame with
  // no message.id (older history, bare payload) is taken as the next block
  // of whatever is streaming, like a match.
  const id = obj(p.message)?.id
  if (partial.messageId !== null && typeof id === 'string' && id !== partial.messageId) return { ...s, partial: null }
  // By counter, not lowest index: after a snapshot {blocks:[1]} the replayed
  // assistant 0 must be a no-op that leaves block 1 streaming (spec D1).
  const { [partial.finalized]: _finalized, ...blocks } = partial.blocks
  return { ...s, partial: { ...partial, finalized: partial.finalized + 1, blocks } }
}

function recordToolStarts(s: ExecutionState, p: Record<string, unknown>, at: number): ExecutionState {
  let tools = s.tools
  for (const b of contentBlocks(p)) {
    if (b.type !== 'tool_use' || typeof b.id !== 'string' || tools[b.id]) continue
    tools = { ...tools, [b.id]: { name: typeof b.name === 'string' ? b.name : '', startedAt: at, endedAt: null, status: 'running' } }
  }
  return tools === s.tools ? s : { ...s, tools }
}

function recordToolEnds(s: ExecutionState, p: Record<string, unknown>, at: number): ExecutionState {
  let tools = s.tools
  for (const b of contentBlocks(p)) {
    if (b.type !== 'tool_result' || typeof b.tool_use_id !== 'string') continue
    const t = tools[b.tool_use_id]
    // done/error are final; an 'aborted' tool (A3 fired on a turn-ending
    // event before its tool_result landed) is still corrected by that result.
    if (!t || t.status === 'done' || t.status === 'error') continue
    tools = { ...tools, [b.tool_use_id]: { ...t, endedAt: at, status: b.is_error === true ? 'error' : 'done' } }
  }
  return tools === s.tools ? s : { ...s, tools }
}

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
    if (ev.kind === 'result') next = { ...next, pendingSend: false }
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

function obj(v: unknown): Record<string, unknown> | null {
  return v && typeof v === 'object' && !Array.isArray(v) ? (v as Record<string, unknown>) : null
}

function blockIndex(v: unknown): number | null {
  return typeof v === 'number' && Number.isInteger(v) && v >= 0 ? v : null
}

function blockType(v: unknown): PartialBlock['type'] {
  return v === 'text' || v === 'thinking' || v === 'tool_use' ? v : 'unknown'
}

function newBlock(index: number, type: PartialBlock['type']): PartialBlock {
  return { index, type, text: '', thinking: '', partialJson: '' }
}

/**
 * How many blocks of `messageId` this client has already rendered as durable
 * `assistant` frames (F5: one frame per block). This — not the snapshot's
 * lowest index — seeds `finalized`, because the replay that follows a
 * snapshot re-sends exactly those frames and D1 finalizes by index.
 */
export function finalizedFor(s: ExecutionState, messageId: string): number {
  let n = 0
  for (const m of s.messages) {
    if (m.type !== 'assistant') continue
    const raw = m as unknown as Record<string, unknown>
    if (raw.parent_tool_use_id != null) continue
    const message = obj(raw.message)
    if (message && message.id === messageId) n += 1
  }
  return n
}

function withPartial(s: ExecutionState, partial: PartialAssembly, turnLive = s.turnLive): ExecutionState {
  return { ...s, partial, turnLive }
}

function setBlock(s: ExecutionState, partial: PartialAssembly, block: PartialBlock): ExecutionState {
  return withPartial(s, { ...partial, blocks: { ...partial.blocks, [block.index]: block } })
}

const DELTA_FIELD: Record<string, { type: PartialBlock['type']; key: 'text' | 'thinking' | 'partial_json'; field: 'text' | 'thinking' | 'partialJson' }> = {
  text_delta: { type: 'text', key: 'text', field: 'text' },
  thinking_delta: { type: 'thinking', key: 'thinking', field: 'thinking' },
  input_json_delta: { type: 'tool_use', key: 'partial_json', field: 'partialJson' },
}

function applyStreamEvent(s: ExecutionState, event: Record<string, unknown>): ExecutionState {
  const orphan: PartialAssembly = { messageId: null, finalized: 0, blocks: {} }
  switch (event.type) {
    case 'message_start': {
      const id = obj(event.message)?.id
      if (typeof id !== 'string') return s
      if (s.partial?.messageId === id) return s
      return withPartial(s, { messageId: id, finalized: finalizedFor(s, id), blocks: {} }, true)
    }
    case 'content_block_start': {
      const index = blockIndex(event.index)
      const cb = obj(event.content_block)
      if (index === null || !cb) return s
      const partial = s.partial ?? orphan
      if (index < partial.finalized) return s
      const block = newBlock(index, blockType(cb.type))
      if (block.type === 'tool_use') {
        if (typeof cb.id === 'string') block.toolId = cb.id
        if (typeof cb.name === 'string') block.toolName = cb.name
      }
      return setBlock(s, partial, block)
    }
    case 'content_block_delta': {
      const index = blockIndex(event.index)
      const delta = obj(event.delta)
      if (index === null || !delta) return s
      const spec = typeof delta.type === 'string' ? DELTA_FIELD[delta.type] : undefined
      if (!spec) return s
      const piece = delta[spec.key]
      if (typeof piece !== 'string') return s
      const partial = s.partial ?? orphan
      if (index < partial.finalized) return s
      const prev = partial.blocks[index] ?? newBlock(index, spec.type)
      return setBlock(s, partial, { ...prev, [spec.field]: prev[spec.field] + piece })
    }
    default:
      return s
  }
}

function applySnapshot(s: ExecutionState, payload: Record<string, unknown>): ExecutionState {
  const messageId = payload.message_id
  if (typeof messageId !== 'string' || !Array.isArray(payload.blocks)) return s
  const blocks: Record<number, PartialBlock> = {}
  for (const raw of payload.blocks) {
    const b = obj(raw)
    const index = b ? blockIndex(b.index) : null
    if (!b || index === null) continue
    const text = typeof b.text === 'string' ? b.text : ''
    const thinking = typeof b.thinking === 'string' ? b.thinking : ''
    const partialJson = typeof b.partial_json === 'string' ? b.partial_json : ''
    const inferred: PartialBlock['type'] = thinking ? 'thinking' : partialJson ? 'tool_use' : text ? 'text' : 'unknown'
    const type = b.type === undefined ? inferred : blockType(b.type)
    blocks[index] = { index, type, text, thinking, partialJson }
  }
  return withPartial(s, { messageId, finalized: finalizedFor(s, messageId), blocks }, true)
}

/**
 * Transient frames (no SSE id) → the partial assembly (spec §4.1 T1–T8).
 * Returns `s` itself whenever nothing changes so the store can skip
 * materialising an entry. Never throws on malformed payloads.
 */
export function applyTransientFrame(s: ExecutionState, kind: string, payload: Record<string, unknown>): ExecutionState {
  const p = obj(payload)
  if (!p || p.parent_tool_use_id != null) return s
  if (kind === 'stream_event') {
    const event = obj(p.event)
    return event && typeof event.type === 'string' ? applyStreamEvent(s, event) : s
  }
  if (kind === 'stream_snapshot') return applySnapshot(s, p)
  return s
}
