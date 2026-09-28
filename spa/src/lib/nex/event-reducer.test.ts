// spa/src/lib/nex/event-reducer.test.ts
import { describe, it, expect } from 'vitest'
import { applyDurableEvent, applyTasksSnapshot, applyTransientFrame, defaultExecutionState, frameToEvent, hasOpenTurn, isLifecycleKind, isTaskEventKind, lastEndedOutcome, type ExecutionState } from './event-reducer'
import { parseTask } from './tasks'
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

  it('lastEventAt tracks the newest applied non-task event created_at, max-monotonic', () => {
    const at = (seq: number, kind: string, created_at: number, payload: Record<string, unknown> = {}): NexEvent =>
      ({ seq, execution_id: 'exc_1', kind, payload, created_at })
    let s = defaultExecutionState()
    expect(s.lastEventAt).toBe(0)
    s = applyDurableEvent(s, at(1, 'assistant', 100, { type: 'assistant' }))
    expect(s.lastEventAt).toBe(100)
    s = applyDurableEvent(s, at(2, 'tool_use', 150, { tool_use_id: 'x', parent_tool_use_id: null, name: 'Bash' }))
    expect(s.lastEventAt).toBe(150)
    // A higher seq with an older (or missing) stamp never lowers it.
    s = applyDurableEvent(s, at(3, 'assistant', 120, { type: 'assistant' }))
    s = applyDurableEvent(s, at(4, 'assistant', 0, { type: 'assistant' }))
    expect(s.lastEventAt).toBe(150)
    // A replayed (seq-guarded) event does not move it.
    s = applyDurableEvent(s, at(2, 'assistant', 999, { type: 'assistant' }))
    expect(s.lastEventAt).toBe(150)
    // Task events bypass the seq guard and never move it.
    s = applyDurableEvent(s, at(9, 'task_start', 999, { task_id: 't1', kind: 'shell' }))
    expect(s.lastEventAt).toBe(150)
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

// ---- worker pane R1 T5.3 (#1228): a subagent's tool events are kept --------

describe('applyDurableEvent: subagent tool events (#1228)', () => {
  const at = (seq: number, kind: string, payload: Record<string, unknown>, created_at = seq * 100): NexEvent =>
    ({ seq, execution_id: 'exc_1', kind, payload, created_at })
  const PARENT = 'toolu_task'
  const childToolUse = (id: string, name = 'Read') => ({
    type: 'assistant', parent_tool_use_id: PARENT,
    message: { id: 'msg_child', role: 'assistant', content: [{ type: 'tool_use', id, name, input: { file_path: '/a' } }], stop_reason: 'tool_use' },
  })
  const childToolResult = (id: string) => ({
    type: 'user', parent_tool_use_id: PARENT,
    message: { role: 'user', content: [{ type: 'tool_result', tool_use_id: id, content: 'ok' }] },
  })
  const mainPartial: ExecutionState['partial'] = {
    messageId: 'msg_main', finalized: 0,
    blocks: { 0: { index: 0, type: 'text', text: 'main says', thinking: '', partialJson: '' } },
  }
  const live = (): ExecutionState => ({ ...defaultExecutionState(), turnLive: true })

  it("records a subagent's tool timing", () => {
    let s = applyDurableEvent(live(), at(1, 'assistant', childToolUse('toolu_c1')))
    expect(s.tools.toolu_c1).toEqual({ name: 'Read', startedAt: 100, endedAt: null, status: 'running' })
    s = applyDurableEvent(s, at(2, 'user', childToolResult('toolu_c1')))
    expect(s.tools.toolu_c1).toEqual({ name: 'Read', startedAt: 100, endedAt: 200, status: 'done' })
    // Still a message each, as before.
    expect(s.messages).toHaveLength(2)
  })

  it("a subagent's result does not end the main turn", () => {
    let s = applyDurableEvent(live(), at(1, 'assistant', childToolUse('toolu_c1')))
    s = applyDurableEvent(s, at(2, 'result', { type: 'result', subtype: 'success', parent_tool_use_id: PARENT }))
    expect(s.turnLive).toBe(true)
    expect(s.tools.toolu_c1.status).toBe('running')
  })

  it("a subagent's frames do not touch the main partial", () => {
    const base: ExecutionState = { ...live(), partial: mainPartial }
    let s = applyDurableEvent(base, at(1, 'assistant', childToolUse('toolu_c1')))
    s = applyDurableEvent(s, at(2, 'user', childToolResult('toolu_c1')))
    s = applyDurableEvent(s, at(3, 'tool_use', { tool_use_id: 'toolu_c1', parent_tool_use_id: PARENT, name: 'Read' }))
    s = applyDurableEvent(s, at(4, 'result', { type: 'result', subtype: 'success', parent_tool_use_id: PARENT }))
    expect(s.partial).toBe(base.partial)
    expect(s.turnLive).toBe(true)
    // A child frame never opens a turn either.
    expect(s.turnStarts).toEqual([])
  })

  it("a subagent's N2 facts land on its own tool entry", () => {
    const base: ExecutionState = { ...live(), tools: { [PARENT]: { name: 'Task', startedAt: 50, endedAt: null, status: 'running' } } }
    let s = applyDurableEvent(base, at(1, 'assistant', childToolUse('toolu_c1')))
    s = applyDurableEvent(s, at(2, 'tool_use', { tool_use_id: 'toolu_c1', parent_tool_use_id: PARENT, name: 'Read', known: true, primary_arg: { key: 'file_path', value: '/a' } }))
    s = applyDurableEvent(s, at(3, 'user', childToolResult('toolu_c1')))
    s = applyDurableEvent(s, at(4, 'tool_result', { tool_use_id: 'toolu_c1', parent_tool_use_id: PARENT, status: 'ok', duration_ms: 5, file: { path: '/a', lines: 12 } }))
    expect(s.tools.toolu_c1).toEqual({
      name: 'Read', startedAt: 100, endedAt: 300, status: 'done',
      known: true, primaryArg: { key: 'file_path', value: '/a' }, durationMs: 5, file: { path: '/a', lines: 12 },
    })
    // The parent Task entry is not the child's home.
    expect(s.tools[PARENT]).toEqual(base.tools[PARENT])
    // N2 kinds are still never messages.
    expect(s.messages).toHaveLength(2)
    expect(s.lastSeq).toBe(4)
  })

  it("endTurn still aborts a child's running tool", () => {
    let s = applyDurableEvent(live(), at(1, 'assistant', childToolUse('toolu_c1')))
    s = applyDurableEvent(s, at(2, 'result', { type: 'result', subtype: 'success', parent_tool_use_id: null }))
    expect(s.turnLive).toBe(false)
    expect(s.tools.toolu_c1).toEqual({ name: 'Read', startedAt: 100, endedAt: 200, status: 'aborted' })
  })
})

describe('task events (nexen v0.13 task_start / task_end)', () => {
  const start = (task_id: string, over: Record<string, unknown> = {}) => ({
    task_id, turn_id: 'turn_1', kind: 'shell', task_type: 'local_bash', tool_use_id: `toolu_${task_id}`, parent_tool_use_id: null,
    description: `run ${task_id}`, command: 'sleep 8', backgrounded: true, started_at: 1000, ...over,
  })
  const end = (task_id: string, over: Record<string, unknown> = {}) => ({
    task_id, turn_id: 'turn_1', kind: 'shell', tool_use_id: `toolu_${task_id}`, status: 'completed', provider_status: 'completed',
    ended_at: 2000, closed_by: 'provider', cost_usd: null, ...over,
  })
  const live = (): ExecutionState => {
    const s = applyDurableEvent(defaultExecutionState(), ev(1, 'execution.message_accepted', { text: 'go' }))
    return { ...s, pendingSend: true, partial: { messageId: 'msg_1', finalized: 0, blocks: {} } }
  }

  it('isTaskEventKind is exactly task_start / task_end', () => {
    expect(isTaskEventKind('task_start')).toBe(true)
    expect(isTaskEventKind('task_end')).toBe(true)
    expect(isTaskEventKind('task_updated')).toBe(false)
    expect(isTaskEventKind('tool_use')).toBe(false)
  })

  it('never appended to messages, never a turn boundary, never touches partial / pendingSend / turnLive / summary', () => {
    const before = live()
    let s = applyDurableEvent(before, ev(2, 'task_start', start('t1')))
    s = applyDurableEvent(s, ev(3, 'task_end', end('t1')))
    // Task events never move the shared high-water mark (SSE Last-Event-ID).
    expect(s.lastSeq).toBe(before.lastSeq)
    expect(s.messages).toBe(before.messages)
    expect(s.turnStarts).toBe(before.turnStarts)
    expect(s.partial).toBe(before.partial)
    expect(s.pendingSend).toBe(true)
    expect(s.turnLive).toBe(true)
    expect(s.summaryStale).toBe(before.summaryStale)
    expect(s.tools).toBe(before.tools)
    expect(s.tasks.t1).toMatchObject({ status: 'completed', description: 'run t1', startSeq: 2 })
  })

  it('a shell started inside a subagent (non-null parent_tool_use_id) still lands in tasks', () => {
    const s = applyDurableEvent(live(), ev(2, 'task_start', start('t2', { parent_tool_use_id: 'toolu_agent' })))
    expect(s.tasks.t2).toMatchObject({ status: 'running', parent_tool_use_id: 'toolu_agent' })
    expect(s.turnLive).toBe(true)
  })

  it('a start replayed after its end does not reopen the row', () => {
    let s = applyDurableEvent(defaultExecutionState(), ev(2, 'task_start', start('t1')))
    s = applyDurableEvent(s, ev(3, 'task_end', end('t1')))
    s = applyDurableEvent(s, ev(4, 'task_start', start('t1')))
    expect(s.tasks.t1.status).toBe('completed')
  })

  it('replay then snapshot (#83): a missed task_end is corrected, a row newer than the cursor survives, closure is final', () => {
    let s = defaultExecutionState()
    s = applyDurableEvent(s, ev(5, 'task_start', start('missed')))
    s = applyDurableEvent(s, ev(6, 'task_start', start('done')))
    s = applyDurableEvent(s, ev(12, 'task_end', end('done')))
    s = applyDurableEvent(s, ev(13, 'task_start', start('newer')))
    const snapshot = {
      cursor: 10,
      items: [
        // Read before `done` closed live at seq 12: still says running.
        parseTask({ ...start('done'), status: 'running' }, 10)!,
        // Started before we connected; never seen live.
        parseTask({ ...start('unseen'), status: 'running' }, 10)!,
      ],
    }
    const next = applyTasksSnapshot(s, snapshot)
    expect(Object.keys(next.tasks).sort()).toEqual(['done', 'newer', 'unseen'])
    expect(next.tasks.done.status).toBe('completed')
    expect(next.tasks.newer).toMatchObject({ status: 'running', startSeq: 13 })
    expect(next.tasks.unseen).toMatchObject({ status: 'running', startSeq: 10 })
    // Nothing else moves: the snapshot is not an event.
    expect(next.lastSeq).toBe(s.lastSeq)
    expect(next.messages).toBe(s.messages)
  })

  it('out-of-order live task_end (#83 regressing seq) still closes the row and does not lower lastSeq', () => {
    let s = applyDurableEvent(defaultExecutionState(), ev(2, 'task_start', start('t1')))
    s = applyDurableEvent(s, ev(10, 'assistant', { type: 'assistant' }))
    s = applyDurableEvent(s, ev(7, 'task_end', end('t1')))
    expect(s.tasks.t1.status).toBe('completed')
    expect(s.lastSeq).toBe(10)
  })

  it('a task_start replayed with a lower seq after close does not reopen', () => {
    let s = applyDurableEvent(defaultExecutionState(), ev(5, 'task_start', start('t1')))
    s = applyDurableEvent(s, ev(9, 'task_end', end('t1')))
    s = applyDurableEvent(s, ev(5, 'task_start', start('t1')))
    expect(s.tasks.t1.status).toBe('completed')
    expect(s.lastSeq).toBe(0)
  })

  it('a replayed task_end / task_start that changes nothing returns the same state object', () => {
    let s = applyDurableEvent(defaultExecutionState(), ev(3, 'assistant', { type: 'assistant' }))
    s = applyDurableEvent(s, ev(5, 'task_start', start('t1')))
    s = applyDurableEvent(s, ev(9, 'task_end', end('t1')))
    expect(applyDurableEvent(s, ev(9, 'task_end', end('t1')))).toBe(s)
    expect(applyDurableEvent(s, ev(5, 'task_start', start('t1')))).toBe(s)
    const running = applyDurableEvent(s, ev(11, 'task_start', start('t2')))
    expect(applyDurableEvent(running, ev(11, 'task_start', start('t2')))).toBe(running)
    expect(running.lastSeq).toBe(3)
  })

  it('an early task event with a higher seq does not move lastSeq, so a later lower-seq non-task event is still applied', () => {
    let s = applyDurableEvent(defaultExecutionState(), ev(3, 'assistant', { type: 'assistant' }))
    expect(s.lastSeq).toBe(3)
    s = applyDurableEvent(s, ev(10, 'task_start', start('t1')))
    expect(s.lastSeq).toBe(3)
    expect(s.tasks.t1.status).toBe('running')
    const next = applyDurableEvent(s, ev(4, 'assistant', { type: 'assistant', n: 4 }))
    expect(next.lastSeq).toBe(4)
    expect(next.messages).toHaveLength(2)
    expect(next.messages[1]).toMatchObject({ n: 4 })
  })

  it('a non-task event at or below lastSeq is still dropped', () => {
    const s = applyDurableEvent(defaultExecutionState(), ev(5, 'assistant', { type: 'assistant' }))
    expect(applyDurableEvent(s, ev(4, 'assistant', { type: 'assistant' }))).toBe(s)
    expect(applyDurableEvent(s, ev(5, 'assistant', { type: 'assistant' }))).toBe(s)
  })

  it('defaultExecutionState has an empty task table', () => {
    expect(defaultExecutionState().tasks).toEqual({})
  })
})

describe('turnMeta (spec §7.1)', () => {
  const at = (seq: number, kind: string, payload: Record<string, unknown>, created_at: number): NexEvent =>
    ({ seq, execution_id: 'exc_1', kind, payload, created_at })
  const accept = (seq: number, created_at: number) => at(seq, 'execution.message_accepted', { turn_id: `t${seq}`, text: 'go' }, created_at)
  const result = (seq: number, created_at: number, extra: Record<string, unknown> = {}) =>
    at(seq, 'result', { type: 'result', subtype: 'success', is_error: false, parent_tool_use_id: null, ...extra }, created_at)
  // No turn_id: it falls back to the oldest unsealed turn (keyed cases below).
  const terminal = (seq: number, created_at: number, reason: string) =>
    at(seq, 'execution.terminal', { reason, state: 'idle' }, created_at)
  const run = (events: NexEvent[]) => events.reduce(applyDurableEvent, defaultExecutionState())

  it('defaultExecutionState has no turn meta', () => {
    expect(defaultExecutionState().turnMeta).toEqual([])
  })

  it('records ok with duration from result.duration_ms', () => {
    const s = run([accept(1, 1000), result(2, 7000, { duration_ms: 5000 }), terminal(3, 7100, 'final_response')])
    expect(s.turnMeta).toEqual([{ startAt: 1000, endAt: 7000, outcome: 'ok', durationMs: 5000 }])
    expect(s.turnStarts).toEqual([0])
  })

  it('records failed for result.is_error', () => {
    const s = run([accept(1, 1000), result(2, 2000, { is_error: true, duration_ms: 10 }), terminal(3, 2100, 'final_response')])
    expect(s.turnMeta[0].outcome).toBe('failed')
  })

  it('records failed for a non-success result subtype', () => {
    const s = run([accept(1, 1000), result(2, 2000, { subtype: 'error_max_turns' })])
    expect(s.turnMeta[0].outcome).toBe('failed')
  })

  // A result never follows its own turn's lifecycle seal: nexen v0.13.2
  // drains and persists every provider frame (the result included) before
  // concludeTurn emits execution.terminal / execution.interrupted, and an
  // interrupted / error path produces no result at all (nexen@v0.13.2
  // execution/turn.go:282-409). So a result goes only to the oldest unsealed
  // turn; one arriving after every turn is sealed has no turn to stamp and
  // changes nothing (re-review round 2 — no routing back to a sealed turn).
  it('interrupt then result → interrupted; a result after the seal is not routed back to it', () => {
    const s = run([
      accept(1, 1000),
      at(2, 'execution.interrupted', { turn_id: 't1', source: 'user' }, 3000),
      result(3, 3100, { subtype: 'error_during_execution', is_error: true, duration_ms: 99 }),
      terminal(4, 3200, 'interrupted'),
    ])
    expect(s.turnMeta[0]).toMatchObject({ endAt: 3000, outcome: 'interrupted', durationMs: 2000 })
  })

  it('an unsealed turn with a fallback duration: the result duration_ms replaces it (R1-1)', () => {
    const s = run([
      accept(1, 1000),
      at(2, 'execution.terminated', { principal_id: 'p' }, 4000),
      result(3, 4100, { subtype: 'error_during_execution', is_error: true, duration_ms: 2500 }),
    ])
    expect(s.turnMeta[0]).toEqual({ startAt: 1000, endAt: 4000, outcome: 'interrupted', durationMs: 2500 })
  })

  it('turn A sealed without a result, turn B ends with a lone top-level result → B gets it, A untouched', () => {
    const s = run([
      accept(1, 1000),
      at(2, 'execution.terminal', { turn_id: 't1', reason: 'error', state: 'idle' }, 2000),
      accept(3, 5000),
      // Turn B fails to launch: a top-level result, no assistant frame before it.
      result(4, 5200, { subtype: 'error_during_execution', is_error: true, duration_ms: 150 }),
    ])
    expect(s.turnMeta).toEqual([
      { startAt: 1000, endAt: 2000, outcome: 'failed', durationMs: 1000 },
      { startAt: 5000, endAt: 5200, outcome: 'failed', durationMs: 150 },
    ])
  })

  it('a sealed turn that never gets its result does not take the next turn\'s result', () => {
    const s = run([
      accept(1, 1000),
      at(2, 'execution.terminal', { turn_id: 't1', reason: 'error', state: 'idle' }, 2000),
      accept(3, 5000),
      at(4, 'assistant', { type: 'assistant', parent_tool_use_id: null, message: { content: [] } }, 5500),
      result(5, 6000, { duration_ms: 800 }),
    ])
    expect(s.turnMeta).toEqual([
      { startAt: 1000, endAt: 2000, outcome: 'failed', durationMs: 1000 },
      { startAt: 5000, endAt: 6000, outcome: 'ok', durationMs: 800 },
    ])
  })

  it('a result duration is never replaced by a later result, and a non-finite one keeps the fallback', () => {
    const twice = run([accept(1, 1000), result(2, 3000, { duration_ms: 1800 }), result(3, 3500, { duration_ms: 42 })])
    expect(twice.turnMeta[0].durationMs).toBe(1800)
    const bad = run([
      accept(1, 1000),
      at(2, 'execution.terminated', { principal_id: 'p' }, 4000),
      result(3, 4100, { duration_ms: Number.NaN }),
    ])
    expect(bad.turnMeta[0].durationMs).toBe(3000)
  })

  it('result then interrupt → interrupted overrides the result', () => {
    const s = run([
      accept(1, 1000),
      result(2, 3000, { subtype: 'error_during_execution', is_error: true, duration_ms: 1500 }),
      at(3, 'execution.interrupted', { turn_id: 't1', source: 'user' }, 3100),
    ])
    expect(s.turnMeta[0]).toEqual({ startAt: 1000, endAt: 3000, outcome: 'interrupted', durationMs: 1500 })
  })

  it('terminal final_response keeps the result outcome; alone it is ok; terminal interrupted alone → interrupted', () => {
    expect(run([accept(1, 1000), result(2, 1500, { is_error: true }), terminal(3, 2000, 'final_response')]).turnMeta[0].outcome).toBe('failed')
    expect(run([accept(1, 1000), terminal(2, 2000, 'final_response')]).turnMeta[0].outcome).toBe('ok')
    expect(run([accept(1, 1000), terminal(2, 2000, 'interrupted')]).turnMeta[0].outcome).toBe('interrupted')
  })

  it.each(['error', 'session_expired', 'orphaned', 'auth_failed', 'permission_denied', 'context_exhausted', 'quota_exhausted', ''])(
    'terminal reason %j → failed', reason => {
      expect(run([accept(1, 1000), terminal(2, 2000, reason)]).turnMeta[0].outcome).toBe('failed')
    })

  it('unknown terminal reason → failed', () => {
    expect(run([accept(1, 1000), terminal(2, 2000, 'brand_new_reason')]).turnMeta[0].outcome).toBe('failed')
  })

  it('a failure reason after an ok result still reads failed (F3: never hide a failure)', () => {
    const s = run([accept(1, 1000), result(2, 2000, { duration_ms: 900 }), terminal(3, 2100, 'error')])
    expect(s.turnMeta[0]).toEqual({ startAt: 1000, endAt: 2000, outcome: 'failed', durationMs: 900 })
  })

  it.each([
    ['execution.error', 'failed'], ['execution.rejected', 'failed'], ['execution.turn_stalled', 'failed'],
    ['execution.turn_orphaned', 'failed'], ['execution.terminated', 'interrupted'], ['execution.archived', 'interrupted'],
  ])('%s ends a live turn as %s', (kind, outcome) => {
    expect(run([accept(1, 1000), at(2, kind, {}, 2000)]).turnMeta[0].outcome).toBe(outcome)
  })

  it('a lifecycle event after the turn was sealed does not rewrite it (archive / terminate an idle execution)', () => {
    const done = [accept(1, 1000), result(2, 2000, { duration_ms: 900 }), terminal(3, 2100, 'final_response')]
    const s = run([...done, at(4, 'execution.archived', {}, 9000), at(5, 'execution.terminated', { principal_id: 'p' }, 9100)])
    expect(s.turnMeta).toEqual([{ startAt: 1000, endAt: 2000, outcome: 'ok', durationMs: 900 }])
  })

  it('subagent result does not end or stamp the main turn', () => {
    const s = run([accept(1, 1000), result(2, 1500, { parent_tool_use_id: 'toolu_1', is_error: true, duration_ms: 1 })])
    expect(s.turnMeta).toEqual([{ startAt: 1000, endAt: null, outcome: null, durationMs: null }])
    expect(s.turnLive).toBe(true)
  })

  it('duration falls back to endAt - startAt', () => {
    const s = run([accept(1, 1000), result(2, 4500)])
    expect(s.turnMeta[0]).toEqual({ startAt: 1000, endAt: 4500, outcome: 'ok', durationMs: 3500 })
  })

  it('no fallback duration when a timestamp is missing (0)', () => {
    expect(run([accept(1, 0), result(2, 4500)]).turnMeta[0].durationMs).toBeNull()
  })

  it('a non-finite duration_ms falls back', () => {
    expect(run([accept(1, 1000), result(2, 4000, { duration_ms: 'x' })]).turnMeta[0].durationMs).toBe(3000)
  })

  it('stays index-aligned with turnStarts across turns, including delegated', () => {
    const s = run([
      at(1, 'execution.delegated', { brief: 'b' }, 500),
      result(2, 900, { duration_ms: 300 }),
      terminal(3, 950, 'final_response'),
      accept(4, 2000),
      terminal(5, 2600, 'error'),
      accept(6, 3000),
    ])
    expect(s.turnStarts).toHaveLength(3)
    expect(s.turnMeta).toEqual([
      { startAt: 500, endAt: 900, outcome: 'ok', durationMs: 300 },
      { startAt: 2000, endAt: 2600, outcome: 'failed', durationMs: 600 },
      { startAt: 3000, endAt: null, outcome: null, durationMs: null },
    ])
  })

  it('task events do not touch turnMeta', () => {
    const s = run([accept(1, 1000)])
    const next = applyDurableEvent(s, at(9, 'task_start', { task_id: 'x', turn_id: 't', kind: 'shell', started_at: 1 }, 5000))
    expect(next.turnMeta).toBe(s.turnMeta)
  })

  it('replay from scratch yields identical turnMeta', () => {
    const events = [
      accept(1, 1000), result(2, 3000, { duration_ms: 1800 }), terminal(3, 3100, 'final_response'),
      accept(4, 4000), at(5, 'execution.interrupted', { source: 'user' }, 4500), result(6, 4600), terminal(7, 4700, 'interrupted'),
      accept(8, 5000), terminal(9, 5200, 'mystery'),
    ]
    const a = run(events)
    const b = run(events)
    expect(b.turnMeta).toEqual(a.turnMeta)
    expect(a.turnMeta.map(m => m.outcome)).toEqual(['ok', 'interrupted', 'failed'])
  })

  describe('queued turns are keyed by turn_id (fix round 1)', () => {
    const acc = (seq: number, created_at: number, turn_id: string) =>
      at(seq, 'execution.message_accepted', { turn_id, text: 'go' }, created_at)
    const term = (seq: number, created_at: number, turn_id: string, reason: string) =>
      at(seq, 'execution.terminal', { turn_id, reason, state: 'idle' }, created_at)

    it('a send accepted while turn 1 is live gets its own outcome and endAt', () => {
      const s = run([
        acc(1, 1000, 'tA'), acc(2, 1500, 'tB'),
        result(3, 3000, { duration_ms: 1900 }), term(4, 3100, 'tA', 'final_response'),
        result(5, 6000, { is_error: true, duration_ms: 2800 }), term(6, 6100, 'tB', 'error'),
      ])
      expect(s.turnMeta.map(m => m.outcome)).toEqual(['ok', 'failed'])
      expect(s.turnMeta).toEqual([
        { startAt: 1000, endAt: 3000, outcome: 'ok', durationMs: 1900 },
        { startAt: 1500, endAt: 6000, outcome: 'failed', durationMs: 2800 },
      ])
    })

    it('turn_stalled for a withdrawn queued turn stamps that turn, not the live one', () => {
      const s = run([
        acc(1, 1000, 'tA'), acc(2, 1500, 'tB'),
        at(3, 'execution.turn_stalled', { turn_id: 'tB' }, 2000),
        result(4, 3000, { duration_ms: 1900 }), term(5, 3100, 'tA', 'final_response'),
      ])
      expect(s.turnMeta).toEqual([
        { startAt: 1000, endAt: 3000, outcome: 'ok', durationMs: 1900 },
        { startAt: 1500, endAt: 2000, outcome: 'failed', durationMs: 500 },
      ])
    })

    it('a lifecycle event with an unknown turn_id falls back to the oldest unsealed turn', () => {
      const s = run([acc(1, 1000, 'tA'), acc(2, 1500, 'tB'), term(3, 2000, 'nope', 'error')])
      expect(s.turnMeta.map(m => m.outcome)).toEqual(['failed', null])
    })

    it('turn 1 from execution.delegated (no turn_id) binds the terminal turn_id', () => {
      const s = run([
        at(1, 'execution.delegated', { brief: 'b' }, 500),
        term(2, 900, 't1', 'interrupted'),
        at(3, 'execution.interrupted', { turn_id: 't1', source: 'turn_timeout' }, 910),
      ])
      expect(s.turnMeta[0]).toMatchObject({ endAt: 900, outcome: 'failed' })
    })

    it('terminated / archived between a turn\'s ok result and its terminal do not re-mark it', () => {
      const s = run([
        acc(1, 1000, 'tA'), result(2, 2000, { duration_ms: 900 }),
        at(3, 'execution.terminated', { principal_id: 'p' }, 2050),
        at(4, 'execution.archived', {}, 2060),
        term(5, 2100, 'tA', 'final_response'),
      ])
      expect(s.turnMeta).toEqual([{ startAt: 1000, endAt: 2000, outcome: 'ok', durationMs: 900 }])
    })

    it('terminate with a queued send: terminated does not reach the queued turn; its turn_stalled does', () => {
      const s = run([
        acc(1, 1000, 'tA'), acc(2, 1500, 'tB'), result(3, 2000, { duration_ms: 900 }),
        at(4, 'execution.terminated', { principal_id: 'p' }, 2050),
        at(5, 'execution.turn_stalled', { turn_id: 'tB' }, 2060),
        term(6, 2100, 'tA', 'interrupted'),
        at(7, 'execution.interrupted', { turn_id: 'tA', source: 'terminated' }, 2110),
      ])
      expect(s.turnMeta.map(m => m.outcome)).toEqual(['interrupted', 'failed'])
      expect(s.turnMeta[1].endAt).toBe(2060)
    })
  })

  describe('interrupt source (controller ruling: only a user interrupt is hidden)', () => {
    // nexen emits execution.terminal{reason: interrupted} first, then
    // execution.interrupted{turn_id, source} (execution/turn.go:610-621).
    const seqFor = (source: string | undefined) => run([
      at(1, 'execution.message_accepted', { turn_id: 'tA', text: 'go' }, 1000),
      result(2, 2000, { subtype: 'error_during_execution', is_error: true }),
      at(3, 'execution.terminal', { turn_id: 'tA', reason: 'interrupted', state: 'idle' }, 2100),
      at(4, 'execution.interrupted', source === undefined ? { turn_id: 'tA' } : { turn_id: 'tA', source }, 2110),
    ]).turnMeta[0].outcome

    it.each([
      ['user', 'interrupted'], ['terminated', 'interrupted'], [undefined, 'interrupted'],
      ['turn_timeout', 'failed'], ['daemon_shutdown', 'failed'], ['quota', 'failed'], ['brand_new', 'failed'],
    ])('source %j → %s', (source, outcome) => {
      expect(seqFor(source)).toBe(outcome)
    })

    it('an interrupted event before its terminal still decides by source', () => {
      const s = run([
        at(1, 'execution.message_accepted', { turn_id: 'tA', text: 'go' }, 1000),
        at(2, 'execution.interrupted', { turn_id: 'tA', source: 'daemon_shutdown' }, 2000),
        at(3, 'execution.terminal', { turn_id: 'tA', reason: 'interrupted', state: 'idle' }, 2100),
      ])
      expect(s.turnMeta[0].outcome).toBe('failed')
    })
  })

  describe('duplicate events (same seq) change nothing', () => {
    it('a duplicate result', () => {
      const s = run([accept(1, 1000), result(2, 2000, { duration_ms: 900 })])
      const again = applyDurableEvent(s, result(2, 5000, { is_error: true, duration_ms: 1 }))
      expect(again).toBe(s)
    })

    it('a duplicate execution.terminal', () => {
      const s = run([accept(1, 1000), accept(2, 1100), terminal(3, 2000, 'final_response')])
      const again = applyDurableEvent(s, terminal(3, 2500, 'error'))
      expect(again).toBe(s)
      expect(again.turnMeta.map(m => m.outcome)).toEqual(['ok', null])
    })
  })
})

describe('hasOpenTurn / lastEndedOutcome (queued sends)', () => {
  const queued = () => {
    let s = defaultExecutionState()
    s = applyDurableEvent(s, ev(1, 'execution.message_accepted', { text: 'a', turn_id: 'tA' }))
    s = applyDurableEvent(s, ev(2, 'execution.running', { turn_id: 'tA' }))
    s = applyDurableEvent(s, ev(3, 'execution.message_accepted', { text: 'b', turn_id: 'tB' }))
    s = applyDurableEvent(s, ev(4, 'result', { type: 'result', subtype: 'success', is_error: false }))
    s = applyDurableEvent(s, ev(5, 'execution.terminal', { turn_id: 'tA', reason: 'final_response' }))
    return s
  }

  it('no turn yet: nothing open, no outcome', () => {
    expect(hasOpenTurn(defaultExecutionState())).toBe(false)
    expect(lastEndedOutcome(defaultExecutionState())).toBeNull()
  })

  it('a queued turn stays open after the previous turn ends (turnLive is execution-wide)', () => {
    const s = queued()
    expect(s.turnLive).toBe(false)
    expect(hasOpenTurn(s)).toBe(true)
    expect(lastEndedOutcome(s)).toBe('ok')
  })

  it('the queued turn ending closes it and its outcome becomes the last one', () => {
    let s = queued()
    s = applyDurableEvent(s, ev(6, 'execution.running', { turn_id: 'tB' }))
    expect(hasOpenTurn(s)).toBe(true)
    s = applyDurableEvent(s, ev(7, 'result', { type: 'result', subtype: 'error_during_execution', is_error: true }))
    expect(hasOpenTurn(s)).toBe(false)
    expect(lastEndedOutcome(s)).toBe('failed')
  })

  it('an earlier turn still running keeps it open when the queued one is closed by its own keyed end', () => {
    let s = defaultExecutionState()
    s = applyDurableEvent(s, ev(1, 'execution.message_accepted', { text: 'a', turn_id: 'tA' }))
    s = applyDurableEvent(s, ev(2, 'execution.message_accepted', { text: 'b', turn_id: 'tB' }))
    s = applyDurableEvent(s, ev(3, 'execution.turn_stalled', { turn_id: 'tB' }))
    expect(s.turnMeta.map(m => m.outcome)).toEqual([null, 'failed'])
    expect(hasOpenTurn(s)).toBe(true)
    expect(lastEndedOutcome(s)).toBe('failed')
  })
})
