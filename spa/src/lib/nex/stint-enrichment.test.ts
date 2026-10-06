import { describe, it, expect } from 'vitest'
import { ENRICHMENT_EVENT_BUDGET, enrichFromEvents } from './stint-enrichment'
import { derivePrelude } from './prelude'
import { costSummary } from './cost-summary'
import type { StreamMessage } from './message-types'
import type { NexEvent } from './types'

const ev = (seq: number, kind: string, payload: Record<string, unknown> = {}): NexEvent =>
  ({ seq, execution_id: 'exc_A', kind, payload, created_at: 1000 + seq })
const call = (seq: number, id: string, name: string) =>
  ev(seq, 'assistant', { type: 'assistant', parent_tool_use_id: null, message: { role: 'assistant', content: [{ type: 'tool_use', id, name, input: {} }], stop_reason: null } })
const answer = (seq: number, id: string) =>
  ev(seq, 'user', { type: 'user', parent_tool_use_id: null, message: { role: 'user', content: [{ type: 'tool_result', tool_use_id: id, content: 'out' }] } })

describe('enrichFromEvents', () => {
  it('an N2 tool_result with status error and duration_ms 1988 overrides the transcript-derived done', () => {
    // The transcript says the call succeeded (its N2 item: ok, 5 s).
    const view = derivePrelude([
      { offset: null, pos: '1', at: 1, kind: 'tool_use', payload: { tool_use_id: 'toolu_1', name: 'Bash' } },
      { offset: null, pos: '2', at: 2, kind: 'tool_result', payload: { tool_use_id: 'toolu_1', status: 'ok', duration_ms: 5000 } },
    ])
    expect(view.tools.toolu_1).toMatchObject({ status: 'done', durationMs: 5000 })
    const e = enrichFromEvents([
      call(1, 'toolu_1', 'Bash'),
      ev(2, 'tool_use', { tool_use_id: 'toolu_1', parent_tool_use_id: null, name: 'Bash' }),
      answer(3, 'toolu_1'),
      ev(4, 'tool_result', { tool_use_id: 'toolu_1', parent_tool_use_id: null, status: 'error', duration_ms: 1988 }),
    ])
    expect(e.tools.toolu_1).toMatchObject({ name: 'Bash', status: 'error', durationMs: 1988 })
    // The segment's merge: the stint wins by tool_use_id.
    expect({ ...view.tools, ...e.tools }.toolu_1).toMatchObject({ status: 'error', durationMs: 1988 })
    expect(e.truncated).toBe(false)
  })

  it('a task event nests under its tool_use_id', () => {
    const e = enrichFromEvents([
      call(1, 'toolu_T', 'Task'),
      ev(2, 'task_start', { task_id: 'tk1', kind: 'subagent', tool_use_id: 'toolu_T', description: 'look around', started_at: 1000 }),
      ev(3, 'task_end', { task_id: 'tk1', kind: 'subagent', tool_use_id: 'toolu_T', status: 'completed', ended_at: 13000, usage: { total_tokens: 26000, tool_uses: 8, duration_ms: 12000 } }),
      // A shell task has no subagent row.
      ev(4, 'task_start', { task_id: 'tk2', kind: 'shell', tool_use_id: 'toolu_S', command: 'sleep 1' }),
      ev(5, 'task_end', { task_id: 'tk2', kind: 'shell', tool_use_id: 'toolu_S', status: 'completed' }),
    ])
    expect([...e.subagentTasks.keys()]).toEqual(['toolu_T'])
    expect(e.subagentTasks.get('toolu_T')).toMatchObject({ task_id: 'tk1', status: 'completed', description: 'look around', usage: { total_tokens: 26000 } })
  })

  it(`${ENRICHMENT_EVENT_BUDGET + 1} events are truncated, and exactly ${ENRICHMENT_EVENT_BUDGET} are applied`, () => {
    const said = (seq: number) => ev(seq, 'assistant', { type: 'assistant', message: { role: 'assistant', content: [{ type: 'text', text: `line ${seq}` }], stop_reason: null } })
    const events = Array.from({ length: ENRICHMENT_EVENT_BUDGET + 1 }, (_, i) => said(i + 1))
    const cut = enrichFromEvents(events)
    expect(cut.truncated).toBe(true)
    expect(cut.messages).toHaveLength(ENRICHMENT_EVENT_BUDGET)
    expect(cut.messages.at(-1)).toMatchObject({ message: { content: [{ text: `line ${ENRICHMENT_EVENT_BUDGET}` }] } })
    // Exactly the budget is complete; a smaller budget cuts there.
    expect(enrichFromEvents(events.slice(0, ENRICHMENT_EVENT_BUDGET)).truncated).toBe(false)
    const small = enrichFromEvents(events.slice(0, 3), 2)
    expect([small.truncated, small.messages.length]).toEqual([true, 2])
  })

  it('a call or a subagent still running at the end of what was applied is left to the transcript: never a live clock', () => {
    const e = enrichFromEvents([
      call(1, 'toolu_1', 'Bash'),
      answer(2, 'toolu_1'),
      // Its answer lies past the budget.
      call(3, 'toolu_2', 'Bash'),
      ev(4, 'task_start', { task_id: 'tk1', kind: 'subagent', tool_use_id: 'toolu_T' }),
    ])
    expect(Object.keys(e.tools)).toEqual(['toolu_1'])
    expect(e.tools.toolu_1.status).toBe('done')
    expect(e.subagentTasks.size).toBe(0)
  })

  it('keeps a hostile tool id an own key, never the prototype', () => {
    const e = enrichFromEvents([call(1, '__proto__', 'Bash'), answer(2, '__proto__')])
    expect(Object.hasOwn(e.tools, '__proto__')).toBe(true)
    expect(Object.getPrototypeOf(e.tools)).toBe(Object.prototype)
    expect((e.messages as StreamMessage[]).length).toBe(2)
  })
  describe('attachmentsByPrompt', () => {
    const att = (c: string, media_type = 'image/png') => ({ media_type, bytes: 10, sha256: c.repeat(64) })

    it('the k-th entry is the k-th delegated / message_accepted that carried attachments, in event order, parsed by parseAttachmentMeta', () => {
      const e = enrichFromEvents([
        ev(1, 'execution.delegated', { brief: 'two', attachments: [att('a'), att('b', 'image/jpeg')] }),
        ev(2, 'execution.message_accepted', { text: 'none', turn_id: 't2' }),
        ev(3, 'execution.message_accepted', { text: 'empty list', turn_id: 't3', attachments: [] }),
        // Only the valid entries are kept; a list with none valid does not count.
        ev(4, 'execution.message_accepted', { text: 'one valid', turn_id: 't4', attachments: [{ media_type: 'image/png', bytes: 1, sha256: 'NOT-HEX' }, att('c')] }),
        ev(5, 'execution.message_accepted', { text: 'none valid', turn_id: 't5', attachments: [{ media_type: 'image/png', bytes: -1, sha256: 'd'.repeat(64) }] }),
        ev(6, 'execution.message_accepted', { text: '', turn_id: 't6', attachments: [att('e')] }),
      ])
      expect(e.attachmentsByPrompt).toEqual([[att('a'), att('b', 'image/jpeg')], [att('c')], [att('e')]])
    })

    it('other kinds never count, even with an attachments field', () => {
      const e = enrichFromEvents([
        ev(1, 'user', { type: 'user', parent_tool_use_id: null, attachments: [att('a')], message: { role: 'user', content: [{ type: 'tool_result', tool_use_id: 't', content: 'x' }] } }),
        ev(2, 'execution.running', { attachments: [att('b')] }),
      ])
      expect(e.attachmentsByPrompt).toEqual([])
    })

    it('only the applied events count: one past the budget is not read', () => {
      const events = [
        ev(1, 'execution.delegated', { brief: 'x', attachments: [att('a')] }),
        ev(2, 'execution.message_accepted', { text: 'y', turn_id: 't2', attachments: [att('b')] }),
      ]
      expect(enrichFromEvents(events, 1).attachmentsByPrompt).toEqual([[att('a')]])
      expect(enrichFromEvents(events, 2).attachmentsByPrompt).toEqual([[att('a')], [att('b')]])
    })
  })

  describe('costByMessageId', () => {
    const said = (seq: number, id: string, parent: string | null = null) =>
      ev(seq, 'assistant', { type: 'assistant', parent_tool_use_id: parent, message: { id, role: 'assistant', content: [{ type: 'text', text: id }], stop_reason: null } })
    const done = (seq: number, usd: number, extra: Record<string, unknown> = {}) =>
      ev(seq, 'result', { type: 'result', subtype: 'success', total_cost_usd: usd, duration_ms: 1000 * seq, usage: { input_tokens: 1, output_tokens: seq * 10 }, ...extra })

    it('pairs each top-level result with the assistant message ids since the previous one, equal to costSummary turns', () => {
      const e = enrichFromEvents([
        said(1, 'msg_a'), said(2, 'msg_b'), done(3, 0.5),
        said(4, 'msg_c'), done(5, 0.7),
      ])
      const turns = costSummary(e.messages).turns
      expect(turns).toHaveLength(2)
      expect([...e.costByMessageId.keys()]).toEqual(['msg_a', 'msg_b', 'msg_c'])
      expect(e.costByMessageId.get('msg_a')).toEqual(turns[0])
      expect(e.costByMessageId.get('msg_b')).toEqual(turns[0])
      expect(e.costByMessageId.get('msg_c')).toEqual(turns[1])
    })

    it('a subagent frame (parent_tool_use_id set) neither maps nor closes a turn', () => {
      const e = enrichFromEvents([
        said(1, 'msg_a'), said(2, 'msg_sub', 'toolu_T'), done(3, 0.1, { parent_tool_use_id: 'toolu_T' }), done(4, 0.5),
      ])
      expect(e.costByMessageId.has('msg_sub')).toBe(false)
      expect(e.costByMessageId.get('msg_a')?.index).toBe(1)
      expect(costSummary(e.messages).turns).toHaveLength(1)
    })

    it('assistant messages after the last result (a turn still running) and without an id map to nothing', () => {
      const noId = ev(1, 'assistant', { type: 'assistant', parent_tool_use_id: null, message: { role: 'assistant', content: [{ type: 'text', text: 'x' }], stop_reason: null } })
      const e = enrichFromEvents([noId, said(2, 'msg_a'), done(3, 0.5), said(4, 'msg_tail')])
      expect([...e.costByMessageId.keys()]).toEqual(['msg_a'])
    })
  })
})
