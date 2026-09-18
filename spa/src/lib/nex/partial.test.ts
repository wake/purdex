// spa/src/lib/nex/partial.test.ts — spec §4.1 T1–T8 (transient frames) and D1
// (durable assistant frames finalize the partial through applyDurableEvent).
import { describe, it, expect } from 'vitest'
import { applyTransientFrame, finalizedFor, isPartialBlockVisible, partialHasVisibleContent, partialVersionOf, type PartialAssembly, type PartialBlock } from './partial'
import { applyDurableEvent, defaultExecutionState, type ExecutionState } from './event-reducer'
import type { NexEvent } from './types'
import type { AssistantMessage } from './message-types'

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

describe('applyDurableEvent: D1 finalizes the partial', () => {
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
  const transient = (s: ExecutionState, ...frames: Record<string, unknown>[]) =>
    frames.reduce((acc, f) => applyTransientFrame(acc, 'stream_event', f), s)
  const snapshot = (s: ExecutionState, blocks: Record<string, unknown>[], message_id = MSG) =>
    applyTransientFrame(s, 'stream_snapshot', { message_id, blocks })

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

  it('F2: an assistant frame without message.id finalizes the active partial like a matching id', () => {
    let s = transient(defaultExecutionState(), messageStart('m1'), delta(0, { type: 'text_delta', text: 'a' }))
    const noId = (text: string): Record<string, unknown> =>
      ({ type: 'assistant', message: { role: 'assistant', content: [{ type: 'text', text }], stop_reason: null }, parent_tool_use_id: null })
    s = applyDurableEvent(s, at(1, 'assistant', noId('a')))
    expect(s.partial?.blocks[0]).toBeUndefined()
    expect(s.partial?.finalized).toBe(1)
    s = transient(s, blockStart(1, { type: 'text', text: '' }), delta(1, { type: 'text_delta', text: 'b' }))
    expect(s.partial?.blocks[1]?.text).toBe('b')
    s = applyDurableEvent(s, at(2, 'assistant', noId('b')))
    expect(s.partial?.blocks[1]).toBeUndefined()
    expect(s.partial?.finalized).toBe(2)
    expect(s.partial?.messageId).toBe('m1')
  })

})

describe('isPartialBlockVisible (spec §4.4 R1/R3 — one predicate for rendering and state)', () => {
  const block = (over: Partial<PartialBlock> & { type: PartialBlock['type'] }): PartialBlock =>
    ({ index: 0, text: '', thinking: '', partialJson: '', ...over })

  it('text / thinking: visible only when the trimmed content is non-empty', () => {
    expect(isPartialBlockVisible(block({ type: 'text', text: 'a' }))).toBe(true)
    expect(isPartialBlockVisible(block({ type: 'text', text: '' }))).toBe(false)
    expect(isPartialBlockVisible(block({ type: 'text', text: ' \n\t ' }))).toBe(false)
    expect(isPartialBlockVisible(block({ type: 'thinking', thinking: 't' }))).toBe(true)
    expect(isPartialBlockVisible(block({ type: 'thinking', thinking: '  ' }))).toBe(false)
  })

  it('tool_use: a started tool is a visible row even with an empty partialJson', () => {
    expect(isPartialBlockVisible(block({ type: 'tool_use', toolName: 'Bash' }))).toBe(true)
    expect(isPartialBlockVisible(block({ type: 'tool_use' }))).toBe(true)
    expect(isPartialBlockVisible(block({ type: 'tool_use', partialJson: '{' }))).toBe(true)
  })

  it('unknown: never visible, whatever the fields hold', () => {
    expect(isPartialBlockVisible(block({ type: 'unknown', text: 'x', thinking: 'y', partialJson: 'z' }))).toBe(false)
  })
})

describe('partialHasVisibleContent (spec §4.4 R3)', () => {
  const assembly = (blocks: PartialAssembly['blocks']): PartialAssembly => ({ messageId: 'm', finalized: 0, blocks })

  it('null partial and an assembly with no blocks → false', () => {
    expect(partialHasVisibleContent(null)).toBe(false)
    expect(partialHasVisibleContent(assembly({}))).toBe(false)
  })

  it('empty text / thinking and unknown blocks → false', () => {
    const p = assembly({
      0: { index: 0, type: 'text', text: '', thinking: '', partialJson: '' },
      1: { index: 1, type: 'thinking', text: '', thinking: '', partialJson: '' },
      2: { index: 2, type: 'unknown', text: 'x', thinking: 'y', partialJson: 'z' },
    })
    expect(partialHasVisibleContent(p)).toBe(false)
  })

  it('a whitespace-only text block → false (renders no bubble, so the dots must stay)', () => {
    expect(partialHasVisibleContent(assembly({ 0: { index: 0, type: 'text', text: ' \n ', thinking: '', partialJson: '' } }))).toBe(false)
  })

  it('a started tool_use with empty partialJson → true (it renders a spinner row)', () => {
    expect(partialHasVisibleContent(assembly({ 0: { index: 0, type: 'tool_use', text: '', thinking: '', partialJson: '', toolName: 'Bash' } }))).toBe(true)
  })

  it('any block with non-empty text, thinking or partialJson → true', () => {
    const empty = { index: 0, type: 'text' as const, text: '', thinking: '', partialJson: '' }
    expect(partialHasVisibleContent(assembly({ 0: empty, 1: { ...empty, index: 1, text: 'a' } }))).toBe(true)
    expect(partialHasVisibleContent(assembly({ 0: empty, 1: { ...empty, index: 1, type: 'thinking', thinking: 't' } }))).toBe(true)
    expect(partialHasVisibleContent(assembly({ 0: empty, 1: { ...empty, index: 1, type: 'tool_use', partialJson: '{' } }))).toBe(true)
  })
})

describe('partialVersionOf (spec §4.4 R4)', () => {
  const pb = (index: number, over: Partial<PartialBlock> & { type: PartialBlock['type'] }): PartialBlock =>
    ({ index, text: '', thinking: '', partialJson: '', ...over })
  const assembly = (...blocks: PartialBlock[]): PartialAssembly =>
    ({ messageId: 'm', finalized: 0, blocks: Object.fromEntries(blocks.map((b) => [b.index, b])) })

  it('same value for null / undefined, and equal for equal content regardless of object identity', () => {
    expect(partialVersionOf(null)).toBe(partialVersionOf(undefined))
    expect(partialVersionOf(assembly(pb(0, { type: 'text', text: 'hello' })))).toBe(partialVersionOf(assembly(pb(0, { type: 'text', text: 'hello' }))))
    expect(partialVersionOf(assembly())).toBe(partialVersionOf(assembly()))
    expect(partialVersionOf(assembly())).not.toBe(partialVersionOf(null))
  })

  it('changes when text grows', () => {
    expect(partialVersionOf(assembly(pb(0, { type: 'text', text: 'he' })))).not.toBe(partialVersionOf(assembly(pb(0, { type: 'text', text: 'hello' }))))
  })

  it('changes when a tool_use block with empty input is added (the row appears without any delta)', () => {
    const before = assembly(pb(0, { type: 'text', text: 'hi' }))
    const after = assembly(pb(0, { type: 'text', text: 'hi' }), pb(1, { type: 'tool_use', toolName: 'Bash' }))
    expect(partialVersionOf(after)).not.toBe(partialVersionOf(before))
  })

  it('changes when a block is finalized away (fewer blocks, higher finalized)', () => {
    const streaming = assembly(pb(0, { type: 'text', text: 'hi' }))
    const finalized: PartialAssembly = { messageId: 'm', finalized: 1, blocks: {} }
    expect(partialVersionOf(finalized)).not.toBe(partialVersionOf(streaming))
  })

  it('changes when the message id changes with identical blocks', () => {
    const a = assembly(pb(0, { type: 'text', text: 'hi' }))
    const b: PartialAssembly = { ...a, messageId: 'other' }
    expect(partialVersionOf(a)).not.toBe(partialVersionOf(b))
  })

  it('changes when a block changes type or tool name with the same lengths', () => {
    const text = assembly(pb(0, { type: 'text', text: 'ab' }))
    const thinking = assembly(pb(0, { type: 'thinking', thinking: 'ab' }))
    expect(partialVersionOf(text)).not.toBe(partialVersionOf(thinking))
    const bash = assembly(pb(0, { type: 'tool_use', toolName: 'Bash' }))
    const read = assembly(pb(0, { type: 'tool_use', toolName: 'Read' }))
    expect(partialVersionOf(bash)).not.toBe(partialVersionOf(read))
  })
})
