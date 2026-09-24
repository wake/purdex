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
