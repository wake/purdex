// spa/src/lib/nex/event-reducer.test.ts
import { describe, it, expect } from 'vitest'
import { applyDurableEvent, applyTransientFrame, defaultExecutionState, frameToEvent, isLifecycleKind, type ExecutionState } from './event-reducer'
import type { NexEvent, ExecutionSummary } from './types'

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

  it('advances lastSeq on the N2 tool_use / tool_result kinds, never appends them as messages, and overlays them onto tools (P-B3 §4.3)', () => {
    let s = defaultExecutionState()
    s = applyDurableEvent(s, ev(1, 'assistant', { type: 'assistant', message: { role: 'assistant', content: [], stop_reason: null } }))
    s = applyDurableEvent(s, ev(2, 'tool_use', { tool_use_id: 'toolu_1', parent_tool_use_id: null, name: 'Bash', primary_arg: { key: 'command', value: 'ls' } }))
    s = applyDurableEvent(s, ev(3, 'tool_result', { tool_use_id: 'toolu_1', parent_tool_use_id: null, status: 'ok', duration_ms: 12 }))
    expect(s.messages).toHaveLength(1)
    expect(s.lastSeq).toBe(3)
    expect(s.tools.toolu_1).toMatchObject({ name: 'Bash', status: 'done', durationMs: 12, primaryArg: { key: 'command', value: 'ls' } })
    // Not a turn end, not a send acknowledgement either.
    expect(s.pendingSend).toBe(defaultExecutionState().pendingSend)
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

describe('applyDurableEvent: turn composition (D2–D4, subagent guard)', () => {
  const MSG = 'msg_011Cf9fLxdWgh5jkA2b9MjXt'
  const at = (seq: number, kind: string, payload: Record<string, unknown>, created_at = seq * 100): NexEvent =>
    ({ seq, execution_id: 'exc_1', kind, payload, created_at })
  const streamEvent = (event: Record<string, unknown>): Record<string, unknown> =>
    ({ type: 'stream_event', event, session_id: 's', parent_tool_use_id: null, uuid: 'u' })
  const messageStart = (id: string = MSG) =>
    streamEvent({ type: 'message_start', message: { id, type: 'message', role: 'assistant', content: [] } })
  const delta = (index: number, d: Record<string, unknown>) =>
    streamEvent({ type: 'content_block_delta', index, delta: d })
  const transient = (s: ExecutionState, ...frames: Record<string, unknown>[]) =>
    frames.reduce((acc, f) => applyTransientFrame(acc, 'stream_event', f), s)
  const running = (id: string, startedAt = 100) => ({ [id]: { name: 'Bash', startedAt, endedAt: null, status: 'running' as const } })

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

  it('F1: a subagent result (non-null parent_tool_use_id) neither ends the turn nor touches partial / tools, but is still pushed to messages', () => {
    let s = transient(defaultExecutionState(), messageStart(), delta(0, { type: 'text_delta', text: 'a' }))
    s = { ...s, tools: running('toolu_main') }
    const before = s
    s = applyDurableEvent(s, at(1, 'result', { type: 'result', subtype: 'success', parent_tool_use_id: 'toolu_x' }))
    expect(s.partial).toEqual(before.partial)
    expect(s.turnLive).toBe(true)
    expect(s.tools).toEqual(before.tools)
    expect(s.tools.toolu_main.status).toBe('running')
    expect(s.messages).toHaveLength(1)
    expect(s.lastSeq).toBe(1)
  })

  it('F1: a subagent result does not clear pendingSend (the main turn is still in flight)', () => {
    let s = { ...defaultExecutionState(), pendingSend: true }
    s = applyDurableEvent(s, at(1, 'result', { type: 'result', subtype: 'success', parent_tool_use_id: 'toolu_x' }))
    expect(s.pendingSend).toBe(true)
    s = applyDurableEvent(s, at(2, 'result', { type: 'result', subtype: 'success', parent_tool_use_id: null }))
    expect(s.pendingSend).toBe(false)
  })

})

describe('applyDurableEvent: N2 tool events (P-B3 spec §4.3 N0 / N1 / N4)', () => {
  const at = (seq: number, kind: string, payload: Record<string, unknown>, created_at = seq * 100): NexEvent =>
    ({ seq, execution_id: 'exc_1', kind, payload, created_at })
  const assistantWithToolUse = (id: string, name = 'Bash') => ({
    type: 'assistant', parent_tool_use_id: null,
    message: { role: 'assistant', content: [{ type: 'tool_use', id, name, input: { command: 'ls' } }], stop_reason: 'tool_use' },
  })
  const userWithToolResult = (id: string) => ({
    type: 'user', parent_tool_use_id: null,
    message: { role: 'user', content: [{ type: 'tool_result', tool_use_id: id, content: 'ok' }] },
  })
  const running = (id: string, startedAt = 100) => ({ [id]: { name: 'Bash', startedAt, endedAt: null, status: 'running' as const } })

  it('N0: subagent tool_use / tool_result (non-null parent_tool_use_id) leave tools untouched but advance lastSeq', () => {
    const base: ExecutionState = { ...defaultExecutionState(), tools: running('toolu_main') }
    let s = applyDurableEvent(base, at(1, 'tool_use', { tool_use_id: 'toolu_sub', parent_tool_use_id: 'toolu_parent', name: 'Read' }))
    s = applyDurableEvent(s, at(2, 'tool_result', { tool_use_id: 'toolu_sub', parent_tool_use_id: 'toolu_parent', status: 'ok', duration_ms: 5 }))
    expect(s.tools).toEqual(base.tools)
    expect(s.tools.toolu_sub).toBeUndefined()
    expect(s.messages).toEqual([])
    expect(s.lastSeq).toBe(2)
  })

  it('N2 tool_result does not clear pendingSend and leaves turnLive / partial alone (not a turn end, not a message)', () => {
    const partial: ExecutionState['partial'] = { messageId: 'msg_1', finalized: 0, blocks: { 0: { index: 0, type: 'text', text: 'a', thinking: '', partialJson: '' } } }
    const base: ExecutionState = { ...defaultExecutionState(), pendingSend: true, turnLive: true, partial, tools: running('toolu_a') }
    const s = applyDurableEvent(base, at(1, 'tool_result', { tool_use_id: 'toolu_a', parent_tool_use_id: null, status: 'ok', duration_ms: 3 }))
    expect(s.pendingSend).toBe(true)
    expect(s.turnLive).toBe(true)
    expect(s.partial).toBe(partial)
    expect(s.messages).toEqual([])
    expect(s.summaryStale).toBe(false)
    expect(s.tools.toolu_a).toMatchObject({ status: 'done', endedAt: 100, durationMs: 3 })
  })

  it('N1 fail-safe: tool_use with the lower seq creates the entry, the later raw assistant is still a message and A1 skips the entry', () => {
    let s = defaultExecutionState()
    s = applyDurableEvent(s, at(1, 'tool_use', { tool_use_id: 'toolu_1', parent_tool_use_id: null, name: 'Bash' }))
    s = applyDurableEvent(s, at(2, 'assistant', assistantWithToolUse('toolu_1')))
    expect(Object.keys(s.tools)).toEqual(['toolu_1'])
    expect(s.tools.toolu_1).toMatchObject({ name: 'Bash', startedAt: 100, endedAt: null, status: 'running' })
    expect(s.messages).toHaveLength(1)
    expect(s.messages[0]).toMatchObject({ type: 'assistant' })
    expect(s.lastSeq).toBe(2)
  })

  it('N4: an entry finished by the N2 tool_result stays done (with its durationMs) through execution.terminal instead of being aborted', () => {
    let s = defaultExecutionState()
    s = applyDurableEvent(s, at(1, 'assistant', assistantWithToolUse('toolu_1')))
    s = applyDurableEvent(s, at(2, 'tool_use', { tool_use_id: 'toolu_1', parent_tool_use_id: null, name: 'Bash', known: true }))
    s = applyDurableEvent(s, at(3, 'user', userWithToolResult('toolu_1')))
    s = applyDurableEvent(s, at(4, 'tool_result', { tool_use_id: 'toolu_1', parent_tool_use_id: null, status: 'ok', duration_ms: 42 }))
    s = applyDurableEvent(s, at(5, 'execution.terminal', { turn_id: 't', reason: 'completed', state: 'idle' }))
    expect(s.tools.toolu_1).toEqual({ name: 'Bash', startedAt: 100, endedAt: 300, status: 'done', known: true, durationMs: 42 })
    expect(s.turnLive).toBe(false)
    expect(s.messages).toHaveLength(2)
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

describe('turn starts (spec §4.1)', () => {
  const accepted = (seq: number, payload: Record<string, unknown> = {}) =>
    ev(seq, 'execution.message_accepted', { turn_id: `t${seq}`, ...payload })

  it('records a turn start on message_accepted', () => {
    const s = applyDurableEvent(defaultExecutionState(), accepted(1, { text: 'go' }))
    expect(s.turnStarts).toEqual([0])
    expect(s.messages).toHaveLength(1)
  })

  it('records a turn start even when the payload has no text', () => {
    // The site-wide stream strips `text`, so the bubble never arrives — but
    // the turn did open, and the boundary has to survive it.
    const s = applyDurableEvent(defaultExecutionState(), accepted(1))
    expect(s.turnStarts).toEqual([0])
    expect(s.messages).toEqual([])
  })

  it('records a turn start on delegated', () => {
    const s = applyDurableEvent(defaultExecutionState(), ev(1, 'execution.delegated', { brief: 'do the thing' }))
    expect(s.turnStarts).toEqual([0])
  })

  it('records two boundaries at the same index when the first turn appended nothing', () => {
    // A turn whose payload carried no text, ended, and was followed by another
    // turn: both boundaries sit at index 0, and collapsing them would merge two
    // turns the daemon declared separately (spec §4.1).
    let s = applyDurableEvent(defaultExecutionState(), accepted(1))
    s = applyDurableEvent(s, ev(2, 'execution.terminal', { turn_id: 't1' }))
    s = applyDurableEvent(s, accepted(3, { text: 'second' }))
    expect(s.turnStarts).toEqual([0, 0])
    expect(s.messages).toHaveLength(1)
  })

  it('keeps turn starts in ascending order across a history replay', () => {
    const assistantMsg = (seq: number) =>
      ev(seq, 'assistant', { type: 'assistant', message: { role: 'assistant', content: [], stop_reason: null } })
    let s = applyDurableEvent(defaultExecutionState(), ev(1, 'execution.delegated', { brief: 'first' }))
    s = applyDurableEvent(s, assistantMsg(2))
    s = applyDurableEvent(s, accepted(3, { text: 'second' }))
    s = applyDurableEvent(s, assistantMsg(4))
    s = applyDurableEvent(s, accepted(5))
    expect(s.turnStarts).toEqual([0, 2, 4])
    expect(s.messages).toHaveLength(4)
  })

  it('a subagent frame does not record a turn start', () => {
    const s = applyDurableEvent(defaultExecutionState(), ev(1, 'assistant', {
      type: 'assistant', parent_tool_use_id: 'toolu_parent',
      message: { role: 'assistant', content: [], stop_reason: null },
    }))
    expect(s.turnStarts).toEqual([])
  })

  it('turn starts survive a duplicate seq', () => {
    const s = applyDurableEvent(defaultExecutionState(), accepted(1, { text: 'go' }))
    const again = applyDurableEvent(s, accepted(1, { text: 'go' }))
    expect(again).toBe(s)
    expect(again.turnStarts).toEqual([0])
  })

  it('starts with no turn boundaries', () => {
    expect(defaultExecutionState().turnStarts).toEqual([])
  })
})
