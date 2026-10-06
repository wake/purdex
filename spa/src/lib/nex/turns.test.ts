// spa/src/lib/nex/turns.test.ts — spec §4.1: turns come from the boundaries
// the reducer recorded, never from "the next user-looking message".
import { describe, it, expect } from 'vitest'
import type { ContentBlock, StreamMessage, UserMessage } from './message-types'
import type { NexEvent } from './types'
import { INTERRUPT_TEXT, groupTurns, isOpeningLine } from './turns'
import { applyDurableEvent, defaultExecutionState } from './event-reducer'
import { attachmentsOf } from './attachments'

const user = (text: string, parent: string | null = null): StreamMessage => ({
  type: 'user',
  parent_tool_use_id: parent,
  message: { role: 'user', content: [{ type: 'text', text }], stop_reason: null },
})

const toolResult = (): StreamMessage => ({
  type: 'user',
  message: { role: 'user', content: [{ type: 'tool_result', tool_use_id: 't1', content: 'ok' }], stop_reason: null },
})

const assistant = (text: string): StreamMessage => ({
  type: 'assistant',
  message: { role: 'assistant', content: [{ type: 'text', text }], stop_reason: null },
})

describe('groupTurns', () => {
  it('groups a single turn', () => {
    const msgs = [user('hi'), assistant('hello'), assistant('done')]
    expect(groupTurns(msgs, [0])).toEqual([{ start: 0, end: 3, openerIndex: 0, boundary: 0 }])
  })

  it('starts a new turn at each recorded boundary', () => {
    const msgs = [user('a'), assistant('1'), user('b'), assistant('2')]
    expect(groupTurns(msgs, [0, 2])).toEqual([
      { start: 0, end: 2, openerIndex: 0, boundary: 0 },
      { start: 2, end: 4, openerIndex: 2, boundary: 1 },
    ])
  })

  it('puts messages before the first boundary in a leading group', () => {
    const msgs = [assistant('resumed'), user('a'), assistant('1')]
    expect(groupTurns(msgs, [1])).toEqual([
      { start: 0, end: 1, openerIndex: null, boundary: null },
      { start: 1, end: 3, openerIndex: 1, boundary: 0 },
    ])
  })

  it('keeps a boundary whose payload had no text', () => {
    // The second turn's message_accepted carried no text: no user bubble,
    // but the daemon declared a turn — it must not merge into the first.
    const msgs = [user('a'), assistant('1'), assistant('2')]
    expect(groupTurns(msgs, [0, 2])).toEqual([
      { start: 0, end: 2, openerIndex: 0, boundary: 0 },
      { start: 2, end: 3, openerIndex: null, boundary: 1 },
    ])
  })

  it('does not treat a tool_result-only user message as the opening line', () => {
    const msgs = [toolResult(), user('real'), assistant('1')]
    expect(groupTurns(msgs, [0])[0].openerIndex).toBe(1)
  })

  it('does not treat the interrupt sentinel as the opening line', () => {
    const msgs = [user(INTERRUPT_TEXT), assistant('1')]
    expect(groupTurns(msgs, [0])[0].openerIndex).toBeNull()
  })

  it('treats a slash command as the opening line', () => {
    const msgs = [user('/compact'), assistant('1')]
    expect(groupTurns(msgs, [0])[0].openerIndex).toBe(0)
  })

  it('does not treat a subagent prompt as the opening line', () => {
    const msgs = [user('subagent task', 'toolu_1'), user('mine'), assistant('1')]
    expect(groupTurns(msgs, [0])[0].openerIndex).toBe(1)
  })

  it('returns an empty array for an empty list', () => {
    expect(groupTurns([], [])).toEqual([])
  })

  it('ignores a boundary past the end of the list', () => {
    // Clamped to messages.length: it cannot reach past the list or disturb
    // the real turns; at most it is an empty trailing range, the same shape
    // as a just-opened turn whose first message has not arrived yet.
    const msgs = [user('a'), assistant('1')]
    const turns = groupTurns(msgs, [0, 9])
    expect(turns[0]).toEqual({ start: 0, end: 2, openerIndex: 0, boundary: 0 })
    for (const t of turns) expect(t.end).toBeLessThanOrEqual(msgs.length)
    expect(turns.slice(1).every(t => t.start === t.end)).toBe(true)
  })

  it('keeps an empty turn as its own range', () => {
    const msgs = [user('b'), assistant('1')]
    expect(groupTurns(msgs, [0, 0])).toEqual([
      { start: 0, end: 0, openerIndex: null, boundary: 0 },
      { start: 0, end: 2, openerIndex: 0, boundary: 1 },
    ])
  })

  it('boundary maps each turn to its turnStarts index; leading implicit turn has null', () => {
    const msgs = [assistant('resumed'), user('a'), assistant('1'), user('b'), assistant('2')]
    expect(groupTurns(msgs, [1, 3]).map(t => t.boundary)).toEqual([null, 0, 1])
    // A boundary clamped to the end keeps its own index.
    expect(groupTurns(msgs, [0, 3, 9]).map(t => t.boundary)).toEqual([0, 1, 2])
    // An out-of-order list maps each range back to the entry that produced it.
    expect(groupTurns(msgs, [3, 1]).map(t => [t.start, t.boundary])).toEqual([[0, null], [1, 1], [3, 0]])
  })

  it('covers every index exactly once', () => {
    // Deterministic pseudo-random property run (LCG), so a failure reproduces.
    let seed = 12345
    const rand = (n: number): number => {
      seed = (seed * 1103515245 + 12345) & 0x7fffffff
      return seed % n
    }
    for (let run = 0; run < 300; run++) {
      const len = rand(12)
      const msgs = Array.from({ length: len }, (_, i) => (rand(2) ? user(`u${i}`) : assistant(`a${i}`)))
      const starts = Array.from({ length: rand(6) }, () => rand(len + 6) - 3)
      const turns = groupTurns(msgs, starts)
      const seen = new Array<number>(len).fill(0)
      let prevEnd = 0
      for (const t of turns) {
        expect(t.start).toBe(prevEnd)
        expect(t.end).toBeGreaterThanOrEqual(t.start)
        for (let i = t.start; i < t.end; i++) seen[i]++
        if (t.openerIndex !== null) {
          expect(t.openerIndex).toBeGreaterThanOrEqual(t.start)
          expect(t.openerIndex).toBeLessThan(t.end)
        }
        prevEnd = t.end
      }
      if (len > 0) expect(prevEnd).toBe(len)
      expect(seen.every(c => c === 1)).toBe(true)
    }
  })
})

describe('isOpeningLine', () => {
  it('accepts a top-level user text line and rejects everything else', () => {
    expect(isOpeningLine(user('hi'))).toBe(true)
    expect(isOpeningLine(user('hi', 'toolu_1'))).toBe(false)
    expect(isOpeningLine(user(INTERRUPT_TEXT))).toBe(false)
    expect(isOpeningLine(toolResult())).toBe(false)
    expect(isOpeningLine(assistant('hi'))).toBe(false)
  })

  // #1614: a send with only attachments is a transcript line with no text block.
  describe('an attachment-only prompt opens a turn', () => {
    const img = { type: 'image', source: { type: 'omitted', media_type: 'image/png', bytes: 10 } } as const
    const pdf = { type: 'document', source: { type: 'omitted', media_type: 'application/pdf', bytes: 10 } } as const
    const line = (content: ContentBlock[], parent: string | null = null): StreamMessage =>
      ({ type: 'user', parent_tool_use_id: parent, message: { role: 'user', content, stop_reason: null } })

    it('a top-level user line with an image or a document block, with or without text', () => {
      expect(isOpeningLine(line([img]))).toBe(true)
      expect(isOpeningLine(line([pdf]))).toBe(true)
      expect(isOpeningLine(line([img, img, pdf]))).toBe(true)
      expect(isOpeningLine(line([{ type: 'text', text: 'see' }, img]))).toBe(true)
    })

    it('never a tool result\'s carrier, a subagent\'s line, or an assistant line', () => {
      expect(isOpeningLine(line([{ type: 'tool_result', tool_use_id: 't1', content: 'ok' }, img]))).toBe(false)
      expect(isOpeningLine(line([img], 'toolu_1'))).toBe(false)
      expect(isOpeningLine(line([pdf], 'toolu_1'))).toBe(false)
      expect(isOpeningLine({ type: 'assistant', message: { role: 'assistant', content: [img], stop_reason: null } })).toBe(false)
    })

    it('the interrupt sentinel alone still opens nothing', () => {
      expect(isOpeningLine(line([{ type: 'text', text: INTERRUPT_TEXT }]))).toBe(false)
    })

    it('live: the reducer\'s attachment-only bubble opens its turn exactly as before (an empty text block, images on the side)', () => {
      const sha = 'a'.repeat(64)
      const ev = (seq: number, kind: string, payload: Record<string, unknown>): NexEvent => ({ seq, execution_id: 'exc', kind, payload, created_at: seq })
      const s = [
        ev(1, 'execution.delegated', { brief: 'first' }),
        ev(2, 'assistant', { type: 'assistant', parent_tool_use_id: null, message: { role: 'assistant', content: [{ type: 'text', text: 'ok' }], stop_reason: null } }),
        ev(3, 'execution.message_accepted', { text: '', turn_id: 't2', attachments: [{ media_type: 'image/png', bytes: 10, sha256: sha }] }),
      ].reduce(applyDurableEvent, defaultExecutionState())
      const bubble = s.messages[2] as UserMessage
      expect(bubble.message.content).toEqual([{ type: 'text', text: '' }])
      expect(attachmentsOf(bubble)).toEqual([{ media_type: 'image/png', bytes: 10, sha256: sha }])
      expect(isOpeningLine(bubble)).toBe(true)
      expect(groupTurns(s.messages, s.turnStarts)).toEqual([
        { start: 0, end: 2, openerIndex: 0, boundary: 0 },
        { start: 2, end: 3, openerIndex: 2, boundary: 1 },
      ])
    })
  })
})
