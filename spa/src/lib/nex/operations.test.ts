// spa/src/lib/nex/operations.test.ts — spec §4.2: one call is one block, so
// the tool_result that answers a tool_use has to be found across the message
// boundary that separates them.
import { describe, it, expect } from 'vitest'
import type { StreamMessage } from './message-types'
import { blockKey, indexOperations, toolResultText } from './operations'

type Block = Record<string, unknown>

const msg = (role: 'assistant' | 'user', blocks: Block[]): StreamMessage =>
  ({ type: role, message: { role, content: blocks, stop_reason: null } }) as unknown as StreamMessage

const call = (id: string, name = 'Bash'): Block => ({ type: 'tool_use', id, name, input: { command: 'ls' } })
const result = (id: string, content: unknown, isError = false): Block =>
  ({ type: 'tool_result', tool_use_id: id, content, is_error: isError })

describe('indexOperations', () => {
  it('pairs a call with the result in the next message', () => {
    const idx = indexOperations([msg('assistant', [call('X')]), msg('user', [result('X', 'done')])])
    expect(idx.resultForCall.get(blockKey(0, 0))).toEqual({ text: 'done', isError: false })
    expect(idx.consumedResults.has(blockKey(1, 0))).toBe(true)
  })

  it('leaves an unanswered call unpaired', () => {
    const idx = indexOperations([msg('assistant', [call('X')])])
    expect(idx.resultForCall.size).toBe(0)
    expect(idx.consumedResults.size).toBe(0)
  })

  it('records a result whose call is not in the list', () => {
    // A history page boundary: the call is on the page before this one, so
    // the result renders as an orphan rather than disappearing.
    const idx = indexOperations([msg('user', [result('X', 'done')])])
    expect(idx.resultForCall.size).toBe(0)
    expect(idx.consumedResults.size).toBe(0)
  })

  it('gives each of two calls sharing an id its own result', () => {
    const idx = indexOperations([
      msg('assistant', [call('X'), call('X')]),
      msg('user', [result('X', 'first'), result('X', 'second')]),
    ])
    expect(idx.resultForCall.get(blockKey(0, 0))).toEqual({ text: 'first', isError: false })
    expect(idx.resultForCall.get(blockKey(0, 1))).toEqual({ text: 'second', isError: false })
    expect([...idx.consumedResults].sort()).toEqual([blockKey(1, 0), blockKey(1, 1)])
  })

  it('leaves the second of two same-id calls unanswered when only one result arrives', () => {
    const idx = indexOperations([
      msg('assistant', [call('X'), call('X')]),
      msg('user', [result('X', 'only')]),
    ])
    expect(idx.resultForCall.get(blockKey(0, 0))).toEqual({ text: 'only', isError: false })
    expect(idx.resultForCall.has(blockKey(0, 1))).toBe(false)
    expect(idx.resultForCall.size).toBe(1)
  })

  it('leaves a result that precedes its call as an orphan and gives the call the result that follows it', () => {
    // Pairing is forward-only. History is paged in full before the SSE opens
    // and the reducer only accepts increasing seq, so a result can never
    // legitimately precede its own call; a result that does is the tail of a
    // page whose call is off the list, and claiming it would hand the call a
    // stale body while its real answer floated off as an orphan.
    const idx = indexOperations([
      msg('user', [result('X', 'stale')]),
      msg('assistant', [call('X')]),
      msg('user', [result('X', 'fresh')]),
    ])
    expect(idx.resultForCall.get(blockKey(1, 0))).toEqual({ text: 'fresh', isError: false })
    expect(idx.consumedResults.has(blockKey(2, 0))).toBe(true)
    expect(idx.consumedResults.has(blockKey(0, 0))).toBe(false)
    expect(idx.resultForCall.size).toBe(1)
  })

  it('consumes every result exactly once', () => {
    const messages = [
      msg('assistant', [{ type: 'text', text: 'hi' }, call('A'), call('B')]),
      msg('user', [result('B', 'b done'), result('A', 'a done', true)]),
      msg('assistant', [call('A'), call('C')]),
      msg('user', [result('A', 'a again'), result('Z', 'orphan')]),
    ]
    const idx = indexOperations(messages)
    const allResultKeys: string[] = []
    messages.forEach((m, mi) => {
      const blocks = (m as unknown as { message: { content: Block[] } }).message.content
      blocks.forEach((b, bi) => { if (b.type === 'tool_result') allResultKeys.push(blockKey(mi, bi)) })
    })
    for (const key of idx.consumedResults) expect(allResultKeys).toContain(key)
    // One call ↔ one result: nothing is shown twice and nothing is lost.
    expect(idx.resultForCall.size).toBe(idx.consumedResults.size)
    const orphanCount = allResultKeys.filter((k) => !idx.consumedResults.has(k)).length
    expect(idx.consumedResults.size + orphanCount).toBe(allResultKeys.length)
    expect(orphanCount).toBe(1)
    expect(idx.resultForCall.get(blockKey(0, 2))).toEqual({ text: 'b done', isError: false })
    expect(idx.resultForCall.get(blockKey(0, 1))).toEqual({ text: 'a done', isError: true })
    expect(idx.resultForCall.get(blockKey(2, 0))).toEqual({ text: 'a again', isError: false })
    expect(idx.resultForCall.has(blockKey(2, 1))).toBe(false)
  })

  it('ignores a block with no tool_use_id', () => {
    const idx = indexOperations([
      msg('assistant', [{ type: 'tool_use', name: 'Bash' }]),
      msg('user', [{ type: 'tool_result', content: 'stray' }]),
    ])
    expect(idx.resultForCall.size).toBe(0)
    expect(idx.consumedResults.size).toBe(0)
  })

  it('does not let a subagent result answer a main-flow call that shares its id', () => {
    // Main call X, then a subagent frame (parent_tool_use_id P) whose result
    // also names X, then the main result X. Only the same-scope result may
    // answer the call; the subagent one is left as an orphan.
    const scoped = (m: StreamMessage, parent: string | null): StreamMessage =>
      ({ ...m, parent_tool_use_id: parent }) as unknown as StreamMessage
    const idx = indexOperations([
      scoped(msg('assistant', [call('X')]), null),
      scoped(msg('user', [result('X', 'subagent')]), 'P'),
      msg('user', [result('X', 'main')]),
    ])
    expect(idx.resultForCall.get(blockKey(0, 0))).toEqual({ text: 'main', isError: false })
    expect(idx.consumedResults.has(blockKey(1, 0))).toBe(false)
    expect(idx.consumedResults.has(blockKey(2, 0))).toBe(true)
  })

  it('still pairs a call and result inside the same subagent scope', () => {
    const scoped = (m: StreamMessage, parent: string | null): StreamMessage =>
      ({ ...m, parent_tool_use_id: parent }) as unknown as StreamMessage
    const idx = indexOperations([
      scoped(msg('assistant', [call('X')]), 'P'),
      scoped(msg('user', [result('X', 'sub done')]), 'P'),
    ])
    expect(idx.resultForCall.get(blockKey(0, 0))).toEqual({ text: 'sub done', isError: false })
    expect(idx.consumedResults.has(blockKey(1, 0))).toBe(true)
  })
})

// T5.1 (spec §4.5, #1263): a frame whose parent_tool_use_id names a Task call
// belongs to that call's subagent, not to the top level.
describe('indexOperations — the parent link', () => {
  const scoped = (m: StreamMessage, parent: string | null): StreamMessage =>
    ({ ...m, parent_tool_use_id: parent }) as unknown as StreamMessage

  it("collects a subagent's frames under its Task", () => {
    const idx = indexOperations([
      msg('assistant', [call('T', 'Task')]),
      scoped(msg('user', [{ type: 'text', text: 'analyse notes.md' }]), 'T'),
      scoped(msg('assistant', [call('R', 'Read')]), 'T'),
      scoped(msg('user', [result('R', 'contents')]), 'T'),
      msg('user', [result('T', 'hand-back')]),
    ])
    expect(idx.childrenByParent.get('T')).toEqual([1, 2, 3])
    expect(idx.childrenByParent.size).toBe(1)
    // The hand-back is the main flow's answer to the Task call, not a child.
    expect(idx.resultForCall.get(blockKey(0, 0))).toEqual({ text: 'hand-back', isError: false })
  })

  it('keeps child indexes out of the top level', () => {
    const idx = indexOperations([
      msg('user', [{ type: 'text', text: 'please delegate' }]),
      msg('assistant', [call('T', 'Task')]),
      scoped(msg('user', [{ type: 'text', text: 'prompt' }]), 'T'),
      scoped(msg('assistant', [{ type: 'text', text: 'child says' }]), 'T'),
      msg('user', [result('T', 'hand-back')]),
    ])
    expect([...idx.childIndexes].sort()).toEqual([2, 3])
    for (const top of [0, 1, 4]) expect(idx.childIndexes.has(top)).toBe(false)
  })

  it('handles a child whose parent id matches no call in the list', () => {
    // Its Task call is off the list (or never came). Hiding the frame under a
    // call nobody renders would lose it, so it stays at the top level.
    const idx = indexOperations([
      scoped(msg('user', [{ type: 'text', text: 'prompt' }]), 'GONE'),
      scoped(msg('assistant', [{ type: 'text', text: 'child says' }]), 'GONE'),
    ])
    expect(idx.childrenByParent.size).toBe(0)
    expect(idx.childIndexes.size).toBe(0)
  })

  it('does not let a call claim frames that precede it', () => {
    // Forward-only, like the pairing: a child cannot come before the call that
    // spawned it, and a frame naming a later call would make a cycle possible
    // (a message listed under a call inside itself).
    const idx = indexOperations([
      scoped(msg('assistant', [call('T', 'Task')]), 'T'),
      scoped(msg('user', [{ type: 'text', text: 'after' }]), 'T'),
    ])
    expect(idx.childrenByParent.get('T')).toEqual([1])
    expect(idx.childIndexes.has(0)).toBe(false)
  })

  it('keeps children in seq order', () => {
    // Two subagents running side by side interleave their frames with each
    // other and with the main flow; each list is still ascending.
    const idx = indexOperations([
      msg('assistant', [call('A', 'Task'), call('B', 'Task')]),
      scoped(msg('user', [{ type: 'text', text: 'a prompt' }]), 'A'),
      scoped(msg('user', [{ type: 'text', text: 'b prompt' }]), 'B'),
      scoped(msg('assistant', [call('ra', 'Read')]), 'A'),
      scoped(msg('assistant', [call('rb', 'Read')]), 'B'),
      scoped(msg('user', [result('rb', 'b out')]), 'B'),
      scoped(msg('user', [result('ra', 'a out')]), 'A'),
    ])
    expect(idx.childrenByParent.get('A')).toEqual([1, 3, 6])
    expect(idx.childrenByParent.get('B')).toEqual([2, 4, 5])
  })

  it("lists a nested subagent's frames under the inner Task only", () => {
    const idx = indexOperations([
      msg('assistant', [call('T', 'Task')]),
      scoped(msg('assistant', [call('U', 'Task')]), 'T'),
      scoped(msg('user', [{ type: 'text', text: 'inner prompt' }]), 'U'),
    ])
    expect(idx.childrenByParent.get('T')).toEqual([1])
    expect(idx.childrenByParent.get('U')).toEqual([2])
    expect([...idx.childIndexes].sort()).toEqual([1, 2])
  })
})

describe('toolResultText', () => {
  it('keeps a string content as is', () => {
    expect(toolResultText('done')).toBe('done')
  })

  it('flattens a text-block content array', () => {
    expect(toolResultText([{ type: 'text', text: 'done' }])).toBe('done')
  })

  it('joins several text blocks with a newline', () => {
    expect(toolResultText([{ type: 'text', text: 'a' }, { type: 'text', text: 'b' }])).toBe('a\nb')
  })

  it('falls back to JSON for an unknown content shape', () => {
    // A subagent hand-back is exactly this shape (#1263).
    expect(toolResultText({ ok: true })).toBe('{"ok":true}')
    expect(toolResultText([{ type: 'image', source: 'x' }])).toBe('[{"type":"image","source":"x"}]')
  })
})
