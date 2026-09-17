// spa/src/lib/nex/event-reducer.test.ts
import { describe, it, expect } from 'vitest'
import { applyDurableEvent, applyTransientFrame, defaultExecutionState, finalizedFor, frameToEvent, isLifecycleKind, type ExecutionState } from './event-reducer'
import type { NexEvent, ExecutionSummary } from './types'
import type { AssistantMessage } from '../stream-ws'
import wireSample from './__fixtures__/cc-2.1.275-sleep6.jsonl?raw'

const ev = (seq: number, kind: string, payload: Record<string, unknown> = {}): NexEvent =>
  ({ seq, execution_id: 'exc_1', kind, payload, created_at: 1 })

const summary = (extra: Partial<ExecutionSummary> = {}): ExecutionSummary => ({
  id: 'exc_1', state: 'idle', provider: 'claude', principal_id: 'pdx:mlab', cwd: '/w', mount_kind: 'dev',
  brief: 'hi', labels: {}, created_at: 0, updated_at: 0, duration_ms: null, event_count: 0, observers: 0, archived: false, ...extra,
})

describe('applyDurableEvent', () => {
  it('appends provider passthrough kinds as messages and advances lastSeq', () => {
    let s = defaultExecutionState()
    s = applyDurableEvent(s, ev(1, 'assistant', { type: 'assistant', message: { role: 'assistant', content: [], stop_reason: null } }))
    s = applyDurableEvent(s, ev(2, 'some_future_provider_kind', { type: 'some_future_provider_kind' }))
    expect(s.messages).toHaveLength(2)
    expect(s.lastSeq).toBe(2)
  })

  it('is idempotent by seq', () => {
    let s = defaultExecutionState()
    s = applyDurableEvent(s, ev(5, 'assistant', { type: 'assistant' }))
    const again = applyDurableEvent(s, ev(5, 'assistant', { type: 'assistant' }))
    expect(again).toBe(s)
    const older = applyDurableEvent(s, ev(3, 'assistant', { type: 'assistant' }))
    expect(older).toBe(s)
  })

  it('turns execution.delegated brief and message_accepted text into user bubbles, clearing pendingLocal', () => {
    let s: ExecutionState = { ...defaultExecutionState(), pendingLocal: { text: 'and more', delivery: 'queued' } }
    s = applyDurableEvent(s, ev(1, 'execution.delegated', { brief: 'do the thing', principal_id: 'p' }))
    s = applyDurableEvent(s, ev(2, 'execution.message_accepted', { text: 'and more', turn_id: 't1', principal_id: 'p' }))
    expect(s.messages).toEqual([
      { type: 'user', message: { role: 'user', content: [{ type: 'text', text: 'do the thing' }], stop_reason: null } },
      { type: 'user', message: { role: 'user', content: [{ type: 'text', text: 'and more' }], stop_reason: null } },
    ])
    expect(s.pendingLocal).toBeNull()
    expect(s.summaryStale).toBe(true)
  })

  it('skips message_accepted with no text (site-wide stripped) without adding a bubble', () => {
    const s = applyDurableEvent(defaultExecutionState(), ev(1, 'execution.message_accepted', { turn_id: 't1' }))
    expect(s.messages).toEqual([])
    expect(s.lastSeq).toBe(1)
  })

  it('turns an empty-string brief into a user bubble with empty text (human said nothing)', () => {
    const s = applyDurableEvent(defaultExecutionState(), ev(1, 'execution.delegated', { brief: '', principal_id: 'p' }))
    expect(s.messages).toEqual([
      { type: 'user', message: { role: 'user', content: [{ type: 'text', text: '' }], stop_reason: null } },
    ])
  })

  it('skips execution.delegated with no brief key (site-wide stripped) without adding a bubble', () => {
    const s = applyDurableEvent(defaultExecutionState(), ev(1, 'execution.delegated', { principal_id: 'p' }))
    expect(s.messages).toEqual([])
    expect(s.lastSeq).toBe(1)
  })

  it('turns an empty-string message_accepted text into a user bubble with empty text, still clearing pendingLocal', () => {
    let s: ExecutionState = { ...defaultExecutionState(), pendingLocal: { text: '', delivery: 'queued' } }
    s = applyDurableEvent(s, ev(1, 'execution.message_accepted', { text: '', turn_id: 't1', principal_id: 'p' }))
    expect(s.messages).toEqual([
      { type: 'user', message: { role: 'user', content: [{ type: 'text', text: '' }], stop_reason: null } },
    ])
    expect(s.pendingLocal).toBeNull()
  })

  it('skips message_accepted with no text key, still clearing pendingLocal', () => {
    let s: ExecutionState = { ...defaultExecutionState(), pendingLocal: { text: 'and more', delivery: 'queued' } }
    s = applyDurableEvent(s, ev(1, 'execution.message_accepted', { turn_id: 't1', principal_id: 'p' }))
    expect(s.messages).toEqual([])
    expect(s.pendingLocal).toBeNull()
  })

  it('patches summary fields carried by lifecycle events and marks the summary stale', () => {
    let s: ExecutionState = { ...defaultExecutionState(), summary: summary() }
    s = applyDurableEvent(s, ev(1, 'execution.running', {}))
    expect(s.summary?.state).toBe('running')
    expect(s.summaryStale).toBe(true)
    s = applyDurableEvent(s, ev(2, 'execution.observer_attached', { observers: 3, principal_id: 'x' }))
    expect(s.summary?.observers).toBe(3)
    s = applyDurableEvent(s, ev(3, 'execution.terminal', { turn_id: 't1', reason: 'completed', state: 'idle' }))
    expect(s.summary?.last_turn_reason).toBe('completed')
    expect(s.summary?.state).toBe('idle')
    s = applyDurableEvent(s, ev(4, 'execution.terminal', { turn_id: 't2', reason: 'error', state: 'failed', detail: 'boom' }))
    expect(s.summary?.state).toBe('failed')
    s = applyDurableEvent(s, ev(5, 'execution.terminated', { principal_id: 'p' }))
    expect(s.summary?.state).toBe('terminated')
    s = applyDurableEvent(s, ev(6, 'execution.archived', { principal_id: 'p' }))
    expect(s.summary?.archived).toBe(true)
  })

  it('does not invent summary fields when there is no summary yet', () => {
    const s = applyDurableEvent(defaultExecutionState(), ev(1, 'execution.running', {}))
    expect(s.summary).toBeNull()
    expect(s.summaryStale).toBe(true)
  })

  it('lease.acquired/released patch summary.lease only, never the local lease (I11)', () => {
    let s: ExecutionState = { ...defaultExecutionState(), summary: summary(), lease: { leaseId: 'ls_me', expiresAt: 99 } }
    s = applyDurableEvent(s, ev(1, 'lease.acquired', { lease_id: 'ls_other', principal_id: 'pdx:mlab/t-other', expires_at: 50 }))
    expect(s.summary?.lease).toEqual({ principal_id: 'pdx:mlab/t-other', expires_at: 50 })
    expect(s.lease).toEqual({ leaseId: 'ls_me', expiresAt: 99 })
    s = applyDurableEvent(s, ev(2, 'lease.released', { principal_id: 'pdx:mlab/t-other' }))
    expect(s.summary?.lease).toBeUndefined()
    expect(s.lease).toEqual({ leaseId: 'ls_me', expiresAt: 99 })
  })

  it('lease.released with no summary yet does not throw and leaves the local lease untouched', () => {
    const s = applyDurableEvent(defaultExecutionState(), ev(1, 'lease.released', { principal_id: 'p' }))
    expect(s.summary).toBeNull()
    expect(s.summaryStale).toBe(true)
    expect(s.lastSeq).toBe(1)
    expect(s.lease).toBeNull()
  })

  it('result, execution.terminal and execution.error clear pendingSend; sendError is left alone', () => {
    const base: ExecutionState = { ...defaultExecutionState(), pendingSend: true, sendError: { code: 'x', message: 'y' } }
    expect(applyDurableEvent(base, ev(1, 'result', { type: 'result', total_cost_usd: 0.1 })).pendingSend).toBe(false)
    expect(applyDurableEvent(base, ev(1, 'execution.terminal', { turn_id: 't', reason: 'error', state: 'idle' })).pendingSend).toBe(false)
    expect(applyDurableEvent(base, ev(1, 'execution.error', { reason: 'finish_turn_failed' })).pendingSend).toBe(false)
    expect(applyDurableEvent(base, ev(1, 'result', { type: 'result' })).sendError).toEqual({ code: 'x', message: 'y' })
  })

  it('execution.turn_orphaned clears pendingSend (daemon restart mid-turn) without touching pendingLocal', () => {
    const base: ExecutionState = { ...defaultExecutionState(), pendingSend: true, pendingLocal: { text: 'hi', delivery: 'delivered' } }
    const s = applyDurableEvent(base, ev(1, 'execution.turn_orphaned', { turn_id: 't' }))
    expect(s.pendingSend).toBe(false)
    expect(s.pendingLocal).toEqual({ text: 'hi', delivery: 'delivered' })
    expect(s.summaryStale).toBe(true)
  })

  it('execution.turn_stalled clears pendingSend and pendingLocal (queued turn withdrawn on restart)', () => {
    const base: ExecutionState = { ...defaultExecutionState(), pendingSend: true, pendingLocal: { text: 'hi', delivery: 'queued' } }
    const s = applyDurableEvent(base, ev(1, 'execution.turn_stalled', { turn_id: 't' }))
    expect(s.pendingSend).toBe(false)
    expect(s.pendingLocal).toBeNull()
    expect(s.summaryStale).toBe(true)
  })

  it('ignores events with a non-finite seq', () => {
    const s = defaultExecutionState()
    expect(applyDurableEvent(s, { ...ev(0, 'assistant'), seq: Number.NaN })).toBe(s)
  })
})

describe('frameToEvent / isLifecycleKind', () => {
  it('returns null for transient frames and unparsable data', () => {
    expect(frameToEvent({ id: null, event: 'stream_event', data: '{}' })).toBeNull()
    expect(frameToEvent({ id: '7', event: 'assistant', data: '{not json' })).toBeNull()
  })
  it('builds a NexEvent from a Nexen durable frame: bare payload, seq from id:, kind from event: (api/sse.go:295)', () => {
    expect(frameToEvent({ id: '7', event: 'assistant', data: '{"type":"assistant"}' }))
      .toEqual({ seq: 7, execution_id: '', kind: 'assistant', payload: { type: 'assistant' }, created_at: 0 })
    expect(frameToEvent({ id: '9', event: 'execution.terminal', data: '{"turn_id":"t","reason":"completed","state":"idle"}' }))
      .toEqual({ seq: 9, execution_id: '', kind: 'execution.terminal', payload: { turn_id: 't', reason: 'completed', state: 'idle' }, created_at: 0 })
  })
  it('also accepts a full eventView wrapper (history item shape) for forward compatibility', () => {
    expect(frameToEvent({ id: '8', event: 'assistant', data: '{"seq":8,"execution_id":"exc_1","kind":"assistant","payload":{"type":"assistant"},"created_at":9}' }))
      .toEqual({ seq: 8, execution_id: 'exc_1', kind: 'assistant', payload: { type: 'assistant' }, created_at: 9 })
  })
  it('the id: line is authoritative: a wrapper whose seq disagrees with id: is treated as a bare payload, not trusted', () => {
    // A frame `id: 12` carrying `data: {"seq":9999,...}` must not resurrect
    // as seq 9999 — every real event up to 9999 would then be dropped as a
    // duplicate. seq always comes from the id: line; the wrapper's own seq
    // is only a corroborating signal, never a source of truth.
    expect(frameToEvent({ id: '12', event: 'assistant', data: '{"seq":9999,"kind":"assistant","payload":{"type":"assistant"}}' }))
      .toEqual({ seq: 12, execution_id: '', kind: 'assistant', payload: { seq: 9999, kind: 'assistant', payload: { type: 'assistant' } }, created_at: 0 })
  })
  it('treats a bare payload that merely has a "kind" key (no payload object) as the bare-payload path', () => {
    expect(frameToEvent({ id: '11', event: 'assistant', data: '{"kind":"something","type":"assistant"}' }))
      .toEqual({ seq: 11, execution_id: '', kind: 'assistant', payload: { kind: 'something', type: 'assistant' }, created_at: 0 })
  })
  it('classifies kinds', () => {
    expect(isLifecycleKind('execution.running')).toBe(true)
    expect(isLifecycleKind('lease.acquired')).toBe(true)
    expect(isLifecycleKind('assistant')).toBe(false)
    expect(isLifecycleKind('rate_limit_event')).toBe(false)
  })
})

describe('applyTransientFrame', () => {
  const MSG = 'msg_011Cf9fLxdWgh5jkA2b9MjXt'
  const streamEvent = (event: Record<string, unknown>, parent: string | null = null): Record<string, unknown> =>
    ({ type: 'stream_event', event, session_id: 's', parent_tool_use_id: parent, uuid: 'u' })
  const messageStart = (id: string = MSG, parent: string | null = null) =>
    streamEvent({ type: 'message_start', message: { id, type: 'message', role: 'assistant', content: [] } }, parent)
  const blockStart = (index: number, content_block: Record<string, unknown>, parent: string | null = null) =>
    streamEvent({ type: 'content_block_start', index, content_block }, parent)
  const delta = (index: number, d: Record<string, unknown>, parent: string | null = null) =>
    streamEvent({ type: 'content_block_delta', index, delta: d }, parent)
  const assistantMsg = (id: string, parent: string | null = null): AssistantMessage & { parent_tool_use_id: string | null } =>
    ({ type: 'assistant', message: { id, role: 'assistant', content: [{ type: 'text', text: 'a' }], stop_reason: null }, parent_tool_use_id: parent })
  const withMessages = (...msgs: AssistantMessage[]): ExecutionState => ({ ...defaultExecutionState(), messages: msgs })
  const started = () => applyTransientFrame(defaultExecutionState(), 'stream_event', messageStart())

  it('T1: a frame with a non-null parent_tool_use_id (subagent) is ignored and returns the same state object', () => {
    const s = defaultExecutionState()
    expect(applyTransientFrame(s, 'stream_event', messageStart(MSG, 'toolu_parent'))).toBe(s)
    expect(applyTransientFrame(s, 'stream_event', delta(0, { type: 'text_delta', text: 'x' }, 'toolu_parent'))).toBe(s)
  })

  it('T2: message_start for a new id creates an empty assembly seeded by finalizedFor and sets turnLive', () => {
    const s = started()
    expect(s.partial).toEqual({ messageId: MSG, finalized: 0, blocks: {} })
    expect(s.turnLive).toBe(true)
  })

  it('T2: a repeated message_start for the same id returns the same state object (no finalization regression)', () => {
    let s = started()
    s = applyTransientFrame(s, 'stream_event', delta(0, { type: 'text_delta', text: 'hi' }))
    expect(applyTransientFrame(s, 'stream_event', messageStart())).toBe(s)
  })

  it('T2: message_start for a different id replaces the assembly', () => {
    let s = started()
    s = applyTransientFrame(s, 'stream_event', delta(0, { type: 'text_delta', text: 'hi' }))
    s = applyTransientFrame(s, 'stream_event', messageStart('msg_other'))
    expect(s.partial).toEqual({ messageId: 'msg_other', finalized: 0, blocks: {} })
  })

  it('T2: with one matching assistant already in messages, message_start seeds finalized: 1', () => {
    const s = applyTransientFrame(withMessages(assistantMsg(MSG)), 'stream_event', messageStart())
    expect(s.partial?.finalized).toBe(1)
  })

  it('finalizedFor counts only main-agent assistant messages with the same message.id', () => {
    const s = withMessages(assistantMsg(MSG), assistantMsg('msg_other'), assistantMsg(MSG, 'toolu_sub'), assistantMsg(MSG))
    expect(finalizedFor(s, MSG)).toBe(2)
    expect(finalizedFor(s, 'msg_none')).toBe(0)
  })

  it('T3: content_block_start for tool_use records the block with toolId and toolName', () => {
    let s = started()
    s = applyTransientFrame(s, 'stream_event', blockStart(0, { type: 'tool_use', id: 'toolu_1', name: 'Bash', input: {} }))
    expect(s.partial?.blocks[0]).toEqual({ index: 0, type: 'tool_use', text: '', thinking: '', partialJson: '', toolId: 'toolu_1', toolName: 'Bash' })
  })

  it('T3: content_block_start with index < finalized is dropped (same state object)', () => {
    const s = applyTransientFrame(withMessages(assistantMsg(MSG)), 'stream_event', messageStart())
    expect(s.partial?.finalized).toBe(1)
    expect(applyTransientFrame(s, 'stream_event', blockStart(0, { type: 'text', text: '' }))).toBe(s)
  })

  it('T3: content_block_start with partial == null creates an assembly with messageId null and finalized 0', () => {
    const s = applyTransientFrame(defaultExecutionState(), 'stream_event', blockStart(1, { type: 'text', text: '' }))
    expect(s.partial).toEqual({ messageId: null, finalized: 0, blocks: { 1: { index: 1, type: 'text', text: '', thinking: '', partialJson: '' } } })
  })

  it('T4: a delta with no block at its index creates one, type inferred from delta.type', () => {
    let s = started()
    s = applyTransientFrame(s, 'stream_event', delta(0, { type: 'text_delta', text: 'a' }))
    s = applyTransientFrame(s, 'stream_event', delta(1, { type: 'thinking_delta', thinking: 'b' }))
    s = applyTransientFrame(s, 'stream_event', delta(2, { type: 'input_json_delta', partial_json: '{' }))
    expect(s.partial?.blocks[0]).toMatchObject({ index: 0, type: 'text', text: 'a' })
    expect(s.partial?.blocks[1]).toMatchObject({ index: 1, type: 'thinking', thinking: 'b' })
    expect(s.partial?.blocks[2]).toMatchObject({ index: 2, type: 'tool_use', partialJson: '{' })
  })

  it('T4: text, thinking and partial_json deltas append to their own field', () => {
    let s = started()
    s = applyTransientFrame(s, 'stream_event', delta(0, { type: 'text_delta', text: 'hel' }))
    s = applyTransientFrame(s, 'stream_event', delta(0, { type: 'text_delta', text: 'lo' }))
    s = applyTransientFrame(s, 'stream_event', delta(1, { type: 'thinking_delta', thinking: 'hm' }))
    s = applyTransientFrame(s, 'stream_event', delta(1, { type: 'thinking_delta', thinking: 'm' }))
    s = applyTransientFrame(s, 'stream_event', blockStart(2, { type: 'tool_use', id: 'toolu_1', name: 'Bash', input: {} }))
    s = applyTransientFrame(s, 'stream_event', delta(2, { type: 'input_json_delta', partial_json: '' }))
    s = applyTransientFrame(s, 'stream_event', delta(2, { type: 'input_json_delta', partial_json: '{"command": "s' }))
    s = applyTransientFrame(s, 'stream_event', delta(2, { type: 'input_json_delta', partial_json: 'leep 6"}' }))
    expect(s.partial?.blocks[0].text).toBe('hello')
    expect(s.partial?.blocks[1].thinking).toBe('hmm')
    expect(s.partial?.blocks[2]).toMatchObject({ type: 'tool_use', toolName: 'Bash', partialJson: '{"command": "sleep 6"}' })
  })

  it('T4: signature_delta and unknown delta types are absorbed without change', () => {
    let s = started()
    s = applyTransientFrame(s, 'stream_event', delta(0, { type: 'thinking_delta', thinking: 'x' }))
    expect(applyTransientFrame(s, 'stream_event', delta(0, { type: 'signature_delta', signature: 'abc' }))).toBe(s)
    expect(applyTransientFrame(s, 'stream_event', delta(0, { type: 'future_delta', stuff: 1 }))).toBe(s)
  })

  it('T4: a delta with index < finalized is dropped (same state object)', () => {
    const s = applyTransientFrame(withMessages(assistantMsg(MSG)), 'stream_event', messageStart())
    expect(applyTransientFrame(s, 'stream_event', delta(0, { type: 'text_delta', text: 'late' }))).toBe(s)
    const next = applyTransientFrame(s, 'stream_event', delta(1, { type: 'text_delta', text: 'ok' }))
    expect(next.partial?.blocks[1].text).toBe('ok')
  })

  it('T4: a delta before any message_start creates an assembly with messageId null and finalized 0', () => {
    const s = applyTransientFrame(defaultExecutionState(), 'stream_event', delta(0, { type: 'text_delta', text: 'a' }))
    expect(s.partial).toEqual({ messageId: null, finalized: 0, blocks: { 0: { index: 0, type: 'text', text: 'a', thinking: '', partialJson: '' } } })
  })

  it('T5: content_block_stop, message_delta and message_stop return the same state object', () => {
    const s = applyTransientFrame(started(), 'stream_event', delta(0, { type: 'text_delta', text: 'a' }))
    expect(applyTransientFrame(s, 'stream_event', streamEvent({ type: 'content_block_stop', index: 0 }))).toBe(s)
    expect(applyTransientFrame(s, 'stream_event', streamEvent({ type: 'message_delta', delta: { stop_reason: 'end_turn' }, usage: {} }))).toBe(s)
    expect(applyTransientFrame(s, 'stream_event', streamEvent({ type: 'message_stop' }))).toBe(s)
  })

  it('T6: stream_snapshot assigns blocks (not merge), seeds finalized from finalizedFor rather than the lowest index, and sets turnLive', () => {
    let s = applyTransientFrame(withMessages(assistantMsg(MSG)), 'stream_event', delta(5, { type: 'text_delta', text: 'stale' }))
    s = applyTransientFrame(s, 'stream_snapshot', {
      message_id: MSG,
      blocks: [{ index: 2, type: 'text', text: 'b' }, { index: 3, type: 'thinking', thinking: 't' }, { index: 4, type: 'tool_use', partial_json: '{"a' }],
    })
    expect(s.turnLive).toBe(true)
    expect(s.partial?.messageId).toBe(MSG)
    expect(s.partial?.finalized).toBe(1)
    expect(Object.keys(s.partial?.blocks ?? {})).toEqual(['2', '3', '4'])
    expect(s.partial?.blocks[2]).toEqual({ index: 2, type: 'text', text: 'b', thinking: '', partialJson: '' })
    expect(s.partial?.blocks[3]).toEqual({ index: 3, type: 'thinking', text: '', thinking: 't', partialJson: '' })
    expect(s.partial?.blocks[4]).toEqual({ index: 4, type: 'tool_use', text: '', thinking: '', partialJson: '{"a' })
  })

  it('T6: a snapshot with no rendered assistant seeds finalized 0 even when its lowest index is 1', () => {
    const s = applyTransientFrame(defaultExecutionState(), 'stream_snapshot', { message_id: MSG, blocks: [{ index: 1, text: 'b' }] })
    expect(s.partial?.finalized).toBe(0)
    expect(s.partial?.blocks[1]).toMatchObject({ type: 'text', text: 'b' })
  })

  it('T6: snapshot-seeded tool_use blocks carry no toolId / toolName', () => {
    const s = applyTransientFrame(defaultExecutionState(), 'stream_snapshot', { message_id: MSG, blocks: [{ index: 0, type: 'tool_use', partial_json: '{' }] })
    expect(s.partial?.blocks[0].toolName).toBeUndefined()
    expect(s.partial?.blocks[0].toolId).toBeUndefined()
  })

  it('T6: a malformed snapshot (no message_id string / blocks not an array) returns the same state object', () => {
    const s = defaultExecutionState()
    expect(applyTransientFrame(s, 'stream_snapshot', { blocks: [] })).toBe(s)
    expect(applyTransientFrame(s, 'stream_snapshot', { message_id: 7, blocks: [] })).toBe(s)
    expect(applyTransientFrame(s, 'stream_snapshot', { message_id: MSG, blocks: 'nope' })).toBe(s)
    expect(applyTransientFrame(s, 'stream_snapshot', { message_id: MSG })).toBe(s)
  })

  it('T7: lease.renewed and unknown transient kinds return the same state object', () => {
    const s = defaultExecutionState()
    expect(applyTransientFrame(s, 'lease.renewed', { lease_id: 'ls', expires_at: 1 })).toBe(s)
    expect(applyTransientFrame(s, 'something_new', { x: 1 })).toBe(s)
  })

  it('T8: a delta leaves every non-partial field reference-equal', () => {
    const before: ExecutionState = {
      ...defaultExecutionState(),
      lastSeq: 9,
      messages: [assistantMsg('msg_old')],
      summaryStale: true,
      sse: 'open',
      lease: { leaseId: 'ls', expiresAt: 5 },
      leaseError: { code: 'held' },
      pendingSend: true,
      pendingLocal: { text: 'x', delivery: 'queued' },
    }
    const after = applyTransientFrame(before, 'stream_event', delta(0, { type: 'text_delta', text: 'a' }))
    expect(after).not.toBe(before)
    expect(after.lastSeq).toBe(before.lastSeq)
    expect(after.messages).toBe(before.messages)
    expect(after.summaryStale).toBe(before.summaryStale)
    expect(after.sse).toBe(before.sse)
    expect(after.lease).toBe(before.lease)
    expect(after.leaseError).toBe(before.leaseError)
    expect(after.pendingSend).toBe(before.pendingSend)
    expect(after.pendingLocal).toBe(before.pendingLocal)
  })

  it('never throws on garbage payloads and returns the same state object for them', () => {
    const s = started()
    expect(applyTransientFrame(s, 'stream_event', {})).toBe(s)
    expect(applyTransientFrame(s, 'stream_event', { event: 'nope' })).toBe(s)
    expect(applyTransientFrame(s, 'stream_event', { event: { type: 42 } })).toBe(s)
    expect(applyTransientFrame(s, 'stream_event', { event: { type: 'content_block_delta' } })).toBe(s)
    expect(applyTransientFrame(s, 'stream_event', { event: { type: 'content_block_delta', index: 'zero', delta: { type: 'text_delta', text: 'a' } } })).toBe(s)
    expect(applyTransientFrame(s, 'stream_event', { event: { type: 'content_block_delta', index: 0, delta: 'x' } })).toBe(s)
    expect(applyTransientFrame(s, 'stream_event', { event: { type: 'content_block_delta', index: 0, delta: { type: 'text_delta', text: 3 } } })).toBe(s)
    expect(applyTransientFrame(s, 'stream_event', { event: { type: 'content_block_start', index: 0 } })).toBe(s)
    expect(applyTransientFrame(s, 'stream_event', { event: { type: 'message_start', message: {} } })).toBe(s)
    expect(applyTransientFrame(s, 'stream_snapshot', { message_id: MSG, blocks: [null, 'x', { index: 'a' }, { index: 1, text: 'ok' }] }).partial?.blocks)
      .toEqual({ 1: { index: 1, type: 'text', text: 'ok', thinking: '', partialJson: '' } })
  })

  it('does not mutate the previous state or its blocks', () => {
    const s1 = applyTransientFrame(started(), 'stream_event', delta(0, { type: 'text_delta', text: 'a' }))
    const s2 = applyTransientFrame(s1, 'stream_event', delta(0, { type: 'text_delta', text: 'b' }))
    expect(s1.partial?.blocks[0].text).toBe('a')
    expect(s2.partial?.blocks[0].text).toBe('ab')
    expect(s2.partial?.blocks).not.toBe(s1.partial?.blocks)
  })

  it('R5: AssistantMessage.message accepts an id without a cast', () => {
    const m: AssistantMessage = { type: 'assistant', message: { id: 'msg_1', role: 'assistant', content: [], stop_reason: null } }
    const id: string | undefined = m.message.id
    expect(id).toBe('msg_1')
  })
})

describe('applyDurableEvent: partial + tool activity', () => {
  const MSG = 'msg_011Cf9fLxdWgh5jkA2b9MjXt'
  const TOOL = 'toolu_01CyoH2VpvKrWq9hjjeBX6uM'
  const at = (seq: number, kind: string, payload: Record<string, unknown>, created_at = seq * 100): NexEvent =>
    ({ seq, execution_id: 'exc_1', kind, payload, created_at })
  const streamEvent = (event: Record<string, unknown>): Record<string, unknown> =>
    ({ type: 'stream_event', event, session_id: 's', parent_tool_use_id: null, uuid: 'u' })
  const messageStart = (id: string = MSG) =>
    streamEvent({ type: 'message_start', message: { id, type: 'message', role: 'assistant', content: [] } })
  const blockStart = (index: number, content_block: Record<string, unknown>) =>
    streamEvent({ type: 'content_block_start', index, content_block })
  const delta = (index: number, d: Record<string, unknown>) =>
    streamEvent({ type: 'content_block_delta', index, delta: d })
  const assistant = (blocks: Record<string, unknown>[], id: string = MSG, parent: string | null = null): Record<string, unknown> =>
    ({ type: 'assistant', message: { id, role: 'assistant', content: blocks, stop_reason: null }, parent_tool_use_id: parent })
  const toolUse = (id: string = TOOL, name = 'Bash'): Record<string, unknown> => ({ type: 'tool_use', id, name, input: {} })
  const toolResult = (tool_use_id: string = TOOL, is_error = false, parent: string | null = null): Record<string, unknown> =>
    ({ type: 'user', message: { role: 'user', content: [{ type: 'tool_result', tool_use_id, content: 'ok', is_error }] }, parent_tool_use_id: parent })
  const transient = (s: ExecutionState, ...frames: Record<string, unknown>[]) =>
    frames.reduce((acc, f) => applyTransientFrame(acc, 'stream_event', f), s)
  const snapshot = (s: ExecutionState, blocks: Record<string, unknown>[], message_id = MSG) =>
    applyTransientFrame(s, 'stream_snapshot', { message_id, blocks })
  const running = (id: string, startedAt = 100) => ({ [id]: { name: 'Bash', startedAt, endedAt: null, status: 'running' as const } })

  it('D1: the Nth assistant frame finalizes block N (F8/F9 interleaving) leaving finalized == 2 and no blocks', () => {
    let s = transient(defaultExecutionState(),
      messageStart(),
      blockStart(0, { type: 'text', text: '' }),
      delta(0, { type: 'text_delta', text: 'hello' }))
    s = applyDurableEvent(s, at(1, 'assistant', assistant([{ type: 'text', text: 'hello' }])))
    expect(s.partial).toEqual({ messageId: MSG, finalized: 1, blocks: {} })
    s = transient(s,
      streamEvent({ type: 'content_block_stop', index: 0 }),
      blockStart(1, toolUse()),
      delta(1, { type: 'input_json_delta', partial_json: '{"command":"ls"}' }))
    expect(Object.keys(s.partial?.blocks ?? {})).toEqual(['1'])
    s = applyDurableEvent(s, at(2, 'assistant', assistant([toolUse()])))
    expect(s.partial).toEqual({ messageId: MSG, finalized: 2, blocks: {} })
    expect(s.messages).toHaveLength(2)
  })

  it('D1: an assistant frame with a different message id drops the whole partial', () => {
    let s = transient(defaultExecutionState(), messageStart(), delta(0, { type: 'text_delta', text: 'stale' }))
    s = applyDurableEvent(s, at(1, 'assistant', assistant([{ type: 'text', text: 'x' }], 'msg_other')))
    expect(s.partial).toBeNull()
    expect(s.messages).toHaveLength(1)
  })

  it('D1: a null-id assembly (joined mid-message) is finalized by any main-agent assistant frame', () => {
    let s = transient(defaultExecutionState(), delta(0, { type: 'text_delta', text: 'a' }))
    expect(s.partial?.messageId).toBeNull()
    s = applyDurableEvent(s, at(1, 'assistant', assistant([{ type: 'text', text: 'a' }])))
    expect(s.partial).toEqual({ messageId: null, finalized: 1, blocks: {} })
  })

  it('D1: an assistant frame with no partial leaves partial null (no throw, message still pushed)', () => {
    const s = applyDurableEvent(defaultExecutionState(), at(1, 'assistant', assistant([{ type: 'text', text: 'a' }])))
    expect(s.partial).toBeNull()
    expect(s.messages).toHaveLength(1)
  })

  it('D1: a subagent assistant frame (non-null parent_tool_use_id) does not finalize', () => {
    let s = transient(defaultExecutionState(), messageStart(), delta(0, { type: 'text_delta', text: 'a' }))
    s = applyDurableEvent(s, at(1, 'assistant', assistant([{ type: 'text', text: 'a' }], MSG, 'toolu_parent')))
    expect(s.partial).toEqual({ messageId: MSG, finalized: 0, blocks: { 0: { index: 0, type: 'text', text: 'a', thinking: '', partialJson: '' } } })
  })

  it('reconnect case A: snapshot {blocks:[1]} with no rendered assistant, replayed assistant 0 keeps block 1 streaming', () => {
    let s = snapshot(defaultExecutionState(), [{ index: 1, text: 'b' }])
    expect(s.partial?.finalized).toBe(0)
    s = applyDurableEvent(s, at(1, 'assistant', assistant([{ type: 'text', text: 'block zero' }])))
    expect(s.partial?.finalized).toBe(1)
    expect(s.partial?.blocks[1]).toMatchObject({ index: 1, type: 'text', text: 'b' })
    s = transient(s, delta(1, { type: 'text_delta', text: 'c' }))
    expect(s.partial?.blocks[1].text).toBe('bc')
    s = applyDurableEvent(s, at(2, 'assistant', assistant([{ type: 'text', text: 'bc' }])))
    expect(s.partial).toEqual({ messageId: MSG, finalized: 2, blocks: {} })
  })

  it('reconnect case B: assistant 0 already rendered, snapshot {blocks:[1]} seeds finalized 1 and assistant 1 removes block 1', () => {
    let s = applyDurableEvent(defaultExecutionState(), at(1, 'assistant', assistant([{ type: 'text', text: 'block zero' }])))
    s = snapshot(s, [{ index: 1, text: 'b' }])
    expect(s.partial?.finalized).toBe(1)
    expect(s.partial?.blocks[1]).toMatchObject({ text: 'b' })
    s = applyDurableEvent(s, at(2, 'assistant', assistant([{ type: 'text', text: 'b' }])))
    expect(s.partial).toEqual({ messageId: MSG, finalized: 2, blocks: {} })
  })

  it('seq idempotency short-circuits before D1: a replayed assistant frame does not double-finalize', () => {
    let s = transient(defaultExecutionState(), messageStart(), delta(0, { type: 'text_delta', text: 'a' }), delta(1, { type: 'text_delta', text: 'b' }))
    const frame = at(1, 'assistant', assistant([{ type: 'text', text: 'a' }]))
    s = applyDurableEvent(s, frame)
    expect(s.partial?.finalized).toBe(1)
    const again = applyDurableEvent(s, frame)
    expect(again).toBe(s)
    expect(again.partial?.finalized).toBe(1)
    expect(again.partial?.blocks[1]).toBeDefined()
  })

  it('D2: execution.running and execution.message_accepted each set turnLive', () => {
    expect(applyDurableEvent(defaultExecutionState(), at(1, 'execution.running', {})).turnLive).toBe(true)
    expect(applyDurableEvent(defaultExecutionState(), at(1, 'execution.message_accepted', { turn_id: 't', text: 'hi' })).turnLive).toBe(true)
  })

  it.each([
    ['result', { type: 'result', subtype: 'success' }],
    ['execution.terminal', { turn_id: 't', reason: 'completed', state: 'idle' }],
    ['execution.error', { reason: 'boom' }],
    ['execution.rejected', { reason: 'nope' }],
    ['execution.terminated', { principal_id: 'p' }],
    ['execution.interrupted', { turn_id: 't' }],
    ['execution.turn_orphaned', { turn_id: 't' }],
    ['execution.turn_stalled', { turn_id: 't' }],
  ])('D3: %s clears partial, turnLive and aborts running tools', (kind, payload) => {
    const base: ExecutionState = {
      ...defaultExecutionState(),
      turnLive: true,
      partial: { messageId: MSG, finalized: 0, blocks: { 0: { index: 0, type: 'text', text: 'a', thinking: '', partialJson: '' } } },
      tools: running('toolu_a'),
    }
    const s = applyDurableEvent(base, at(7, kind, payload))
    expect(s.partial).toBeNull()
    expect(s.turnLive).toBe(false)
    expect(s.tools.toolu_a).toEqual({ name: 'Bash', startedAt: 100, endedAt: 700, status: 'aborted' })
  })

  it('D4: execution.archived clears partial and turnLive like D3', () => {
    const base: ExecutionState = {
      ...defaultExecutionState(),
      turnLive: true,
      partial: { messageId: MSG, finalized: 0, blocks: {} },
      tools: running('toolu_a'),
    }
    const s = applyDurableEvent(base, at(3, 'execution.archived', { principal_id: 'p' }))
    expect(s.partial).toBeNull()
    expect(s.turnLive).toBe(false)
    expect(s.tools.toolu_a.status).toBe('aborted')
    expect(s.summaryStale).toBe(true)
  })

  it('A1: a tool_use block starts a running activity at ev.created_at; first sighting wins', () => {
    let s = applyDurableEvent(defaultExecutionState(), at(1, 'assistant', assistant([toolUse(TOOL, 'Bash')])))
    expect(s.tools[TOOL]).toEqual({ name: 'Bash', startedAt: 100, endedAt: null, status: 'running' })
    s = applyDurableEvent(s, at(2, 'assistant', assistant([toolUse(TOOL, 'Renamed')], 'msg_2')))
    expect(s.tools[TOOL]).toEqual({ name: 'Bash', startedAt: 100, endedAt: null, status: 'running' })
  })

  it('A1/A2: subagent assistant and user frames (non-null parent_tool_use_id) are skipped', () => {
    let s = applyDurableEvent(defaultExecutionState(), at(1, 'assistant', assistant([toolUse('toolu_sub')], MSG, 'toolu_parent')))
    expect(s.tools).toEqual({})
    s = { ...s, tools: running(TOOL) }
    s = applyDurableEvent(s, at(2, 'user', toolResult(TOOL, false, 'toolu_parent')))
    expect(s.tools[TOOL]).toEqual({ name: 'Bash', startedAt: 100, endedAt: null, status: 'running' })
  })

  it('A2: a tool_result ends the activity as done, or error when is_error', () => {
    const base: ExecutionState = { ...defaultExecutionState(), tools: { ...running('toolu_ok'), ...running('toolu_bad') } }
    let s = applyDurableEvent(base, at(4, 'user', toolResult('toolu_ok', false)))
    s = applyDurableEvent(s, at(5, 'user', toolResult('toolu_bad', true)))
    expect(s.tools.toolu_ok).toEqual({ name: 'Bash', startedAt: 100, endedAt: 400, status: 'done' })
    expect(s.tools.toolu_bad).toEqual({ name: 'Bash', startedAt: 100, endedAt: 500, status: 'error' })
  })

  it('A2: a tool_result for an unknown or already-ended tool changes nothing', () => {
    const ended = { name: 'Bash', startedAt: 100, endedAt: 200, status: 'aborted' as const }
    const base: ExecutionState = { ...defaultExecutionState(), tools: { toolu_x: ended } }
    const s = applyDurableEvent(base, at(4, 'user', toolResult('toolu_x')))
    expect(s.tools.toolu_x).toEqual(ended)
    expect(applyDurableEvent(base, at(5, 'user', toolResult('toolu_never'))).tools).toEqual({ toolu_x: ended })
  })

  it('A3: a turn-ending event aborts only running tools; done/error/aborted keep their timestamps', () => {
    const done = { name: 'Bash', startedAt: 100, endedAt: 200, status: 'done' as const }
    const error = { name: 'Bash', startedAt: 100, endedAt: 250, status: 'error' as const }
    const base: ExecutionState = { ...defaultExecutionState(), tools: { toolu_done: done, toolu_err: error, ...running('toolu_run', 300) } }
    const s = applyDurableEvent(base, at(9, 'execution.turn_orphaned', { turn_id: 't' }))
    expect(s.tools.toolu_done).toEqual(done)
    expect(s.tools.toolu_err).toEqual(error)
    expect(s.tools.toolu_run).toEqual({ name: 'Bash', startedAt: 300, endedAt: 900, status: 'aborted' })
  })

  it('A4: created_at 0 (bare frame fallback) is stored as startedAt 0, never Date.now()', () => {
    const s = applyDurableEvent(defaultExecutionState(), at(1, 'assistant', assistant([toolUse()]), 0))
    expect(s.tools[TOOL].startedAt).toBe(0)
    const ended = applyDurableEvent(s, at(2, 'user', toolResult(), 0))
    expect(ended.tools[TOOL]).toEqual({ name: 'Bash', startedAt: 0, endedAt: 0, status: 'done' })
  })

  it('golden: replaying the CC 2.1.275 sleep-6 wire sample ends with 10 messages, no partial, Bash done', () => {
    const lines = wireSample.split('\n').filter((l) => l.trim() !== '')
    expect(lines).toHaveLength(29)
    const frames = lines.map((l) => JSON.parse(l) as Record<string, unknown>)
    let s = defaultExecutionState()
    let seq = 0
    let expectedJson = ''
    frames.forEach((frame, i) => {
      const line = i + 1
      if (frame.type === 'stream_event') {
        const event = frame.event as { type: string; delta?: { type: string; partial_json?: string } }
        if (event.delta?.type === 'input_json_delta') expectedJson += event.delta.partial_json ?? ''
        s = applyTransientFrame(s, 'stream_event', frame)
      } else {
        seq += 1
        s = applyDurableEvent(s, { seq, execution_id: 'exc_1', kind: frame.type as string, payload: frame, created_at: line * 100 })
      }
      if (line === 13) {
        expect(expectedJson).toBe('{"command": "sleep 6 && echo ok", "description": "Sleep 6 seconds then echo ok"}')
        expect(s.partial?.blocks[0].partialJson).toBe(expectedJson)
        expect(s.partial?.blocks[0].toolName).toBe('Bash')
        expect(s.turnLive).toBe(true)
      }
      if (line === 14) expect(s.partial).toEqual({ messageId: MSG, finalized: 1, blocks: {} })
      if (line === 20) expect(s.tools[TOOL]).toMatchObject({ status: 'done', startedAt: 1400, endedAt: 2000 })
      if (line === 24) expect(s.partial?.blocks[0]).toMatchObject({ type: 'text', text: 'done' })
    })
    expect(seq).toBe(10)
    expect(s.messages).toHaveLength(10)
    const counts: Record<string, number> = {}
    for (const m of s.messages) counts[m.type] = (counts[m.type] ?? 0) + 1
    expect(counts).toEqual({ system: 5, assistant: 2, user: 1, result: 1, rate_limit_event: 1 })
    expect(s.partial).toBeNull()
    expect(s.turnLive).toBe(false)
    expect(s.lastSeq).toBe(10)
    expect(s.pendingSend).toBe(false)
    const bash = s.tools[TOOL]
    expect(bash.status).toBe('done')
    expect(bash.name).toBe('Bash')
    expect(bash.endedAt).not.toBeNull()
    expect(bash.endedAt as number).toBeGreaterThan(bash.startedAt)
  })
})

describe('defaultExecutionState', () => {
  it('starts with no partial assembly, turnLive false and no tool activity', () => {
    const s = defaultExecutionState()
    expect(s.partial).toBeNull()
    expect(s.turnLive).toBe(false)
    expect(s.tools).toEqual({})
  })
})
