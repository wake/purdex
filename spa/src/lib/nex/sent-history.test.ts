import { describe, it, expect } from 'vitest'
import { buildSentHistory } from './sent-history'
import type { StreamMessage } from './message-types'

const user = (text: string, extra: object = {}) =>
  ({ type: 'user', message: { role: 'user', content: [{ type: 'text', text }], stop_reason: null }, ...extra }) as StreamMessage
const SHA = 'b'.repeat(64)

describe('buildSentHistory', () => {
  it('lists the user lines oldest first and splits the [file:] lines off', () => {
    const h = buildSentHistory([user('one'), user('two\n\n[file: /p/a.txt]\n[file: /p/b.txt]')], null)
    expect(h).toEqual([
      { text: 'one', paths: [], images: [] },
      { text: 'two', paths: ['/p/a.txt', '/p/b.txt'], images: [] },
    ])
  })
  it('skips tool results, subagent lines, the interrupt sentinel and injected notifications', () => {
    const toolResult = { type: 'user', message: { role: 'user', content: [{ type: 'tool_result', tool_use_id: 'x', content: 'out' }], stop_reason: null } } as StreamMessage
    const h = buildSentHistory([
      toolResult,
      user('sub', { parent_tool_use_id: 'tu1' }),
      user('[Request interrupted by user]'),
      user('<task-notification>x</task-notification>'),
      user('real'),
    ], null)
    expect(h.map((e) => e.text)).toEqual(['real'])
  })
  it('keeps the native images metadata; an image-only message is an entry', () => {
    const meta = [{ media_type: 'image/png', bytes: 3, sha256: SHA }]
    const h = buildSentHistory([user('', { purdex_attachments: meta })], null)
    expect(h).toEqual([{ text: '', paths: [], images: meta }])
  })
  it('collapses adjacent equal entries and appends the optimistic line', () => {
    const h = buildSentHistory([user('a'), user('a'), user('b')], { text: 'c', delivery: null })
    expect(h.map((e) => e.text)).toEqual(['a', 'b', 'c'])
    expect(buildSentHistory([user('b')], { text: 'b', delivery: 'queued' }).map((e) => e.text)).toEqual(['b'])
  })
})
