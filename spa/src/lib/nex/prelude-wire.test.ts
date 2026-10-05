import { describe, it, expect } from 'vitest'
import { PRELUDE_MAX_PAGE_ITEMS, PRELUDE_MAX_STRING_BYTES, sanitizePreludePage } from './prelude-wire'
import sample from './__fixtures__/prelude-contract-sample.json'

const asst = (pos: string, text: string) => ({
  pos, kind: 'assistant', at: 1759651200123,
  payload: { type: 'assistant', message: { id: 'msg_1', role: 'assistant', content: [{ type: 'text', text }] }, session_id: 's', uuid: 'u' },
})

describe('sanitizePreludePage', () => {
  it('keeps a well-formed ok page in order and maps the cursor', () => {
    const page = sanitizePreludePage({ state: 'ok', items: [asst('10.0', 'a'), asst('20.0', 'b')], prev_cursor: 'c1', total_bytes: 99 })
    expect(page).not.toBeNull()
    expect(page!.items.map((i) => i.pos)).toEqual(['10.0', '20.0'])
    expect(page!.prevCursor).toBe('c1')
    expect(page!.totalBytes).toBe(99)
  })

  it('reads none / gone as terminal states with no items and no cursor', () => {
    expect(sanitizePreludePage({ state: 'none', items: [], prev_cursor: null })).toEqual({ state: 'none', items: [], prevCursor: null, totalBytes: null })
    expect(sanitizePreludePage({ state: 'gone', items: [asst('1', 'x')], prev_cursor: 'c' })).toEqual({ state: 'gone', items: [], prevCursor: null, totalBytes: null })
  })

  it('rejects a body that is not a page', () => {
    expect(sanitizePreludePage(null)).toBeNull()
    expect(sanitizePreludePage({ state: 'weird' })).toBeNull()
    expect(sanitizePreludePage({ state: 'ok', items: [], prev_cursor: 42 })).toBeNull()
    expect(sanitizePreludePage({ state: 'ok', items: [], prev_cursor: 'x'.repeat(257) })).toBeNull()
  })

  it('reads an absent prev_cursor key as null', () => {
    const page = sanitizePreludePage({ state: 'ok', items: [asst('1', 'a')] })
    expect(page).not.toBeNull()
    expect(page!.prevCursor).toBeNull()
  })

  it('rejects a none page whose cursor is malformed', () => {
    expect(sanitizePreludePage({ state: 'none', items: [], prev_cursor: 42 })).toBeNull()
  })

  it('drops items with a bad pos, an unknown kind, or no message, and keeps the first of a duplicated pos', () => {
    const page = sanitizePreludePage({
      state: 'ok', prev_cursor: null,
      items: [
        asst('a:b', 'colon'), asst('x'.repeat(65), 'long'), { ...asst('1', 'unknown'), kind: 'result' },
        { pos: '2', kind: 'user', at: 1, payload: { type: 'user' } },
        asst('3', 'first'), asst('3', 'second'),
      ],
    })
    expect(page!.items).toHaveLength(1)
    expect(page!.items[0]).toMatchObject({ pos: '3', kind: 'assistant' })
  })

  it('turns string content into one text block and forces a top-level frame', () => {
    const page = sanitizePreludePage({
      state: 'ok', prev_cursor: null,
      items: [{ pos: '5', kind: 'user', at: 7, payload: { type: 'user', parent_tool_use_id: 'toolu_x', message: { role: 'user', content: 'hello' } } }],
    })
    const it0 = page!.items[0]
    expect(it0.kind).toBe('user')
    if (it0.kind !== 'user') throw new Error('kind')
    expect(it0.msg).toMatchObject({ type: 'user', parent_tool_use_id: null, message: { role: 'user', content: [{ type: 'text', text: 'hello' }], stop_reason: null } })
    expect(it0.at).toBe(7)
  })

  it('drops tool_use_result / tool_result_meta from user and assistant payloads (spec §4.3)', () => {
    const extra = { tool_use_result: { stdout: 'x' }, tool_result_meta: [{ a: 1 }] }
    const page = sanitizePreludePage({
      state: 'ok', prev_cursor: null,
      items: [
        { pos: '1', kind: 'user', at: 1, payload: { type: 'user', ...extra, message: { role: 'user', content: 'hi' } } },
        { pos: '2', kind: 'assistant', at: 1, payload: { type: 'assistant', ...extra, message: { role: 'assistant', content: [] } } },
      ],
    })
    expect(page!.items).toHaveLength(2)
    for (const it of page!.items) {
      if (it.kind !== 'user' && it.kind !== 'assistant') throw new Error('kind')
      expect(it.msg).not.toHaveProperty('tool_use_result')
      expect(it.msg).not.toHaveProperty('tool_result_meta')
    }
  })

  it('reads N2, segment, compaction and note items', () => {
    const page = sanitizePreludePage({
      state: 'ok', prev_cursor: null,
      items: [
        { pos: '6.1', kind: 'tool_use', at: 1, payload: { tool_use_id: 'toolu_1', name: 'Bash' } },
        { pos: '6.2', kind: 'tool_result', at: 2, payload: { tool_use_id: 'toolu_1', status: 'ok' } },
        { pos: '7', kind: 'prelude.segment', at: 0, payload: { entrypoint: 'cli' } },
        { pos: '8', kind: 'prelude.compaction', at: 0, payload: { trigger: 'auto', pre_tokens: 9 } },
        { pos: '9', kind: 'prelude.note', at: 0, payload: { source: 'command_output', text: 'ok', truncated: true } },
        { pos: '10', kind: 'tool_use', at: 1, payload: { name: 'NoId' } },
      ],
    })
    expect(page!.items.map((i) => i.kind)).toEqual(['tool_use', 'tool_result', 'prelude.segment', 'prelude.compaction', 'prelude.note'])
    expect(page!.items[4]).toMatchObject({ source: 'command_output', text: 'ok', truncated: true })
    expect(page!.items[3]).toMatchObject({ trigger: 'auto' })
  })

  it('cleans every content block so nothing downstream can throw (Review Focus 5)', () => {
    const page = sanitizePreludePage({
      state: 'ok', prev_cursor: null,
      items: [{
        pos: '1', kind: 'assistant', at: 1,
        payload: { type: 'assistant', message: { role: 'assistant', content: [
          { text: 'no type' },
          { type: 'text', text: 42 },
          { type: 'tool_use', id: 't', name: 'Bash', input: 'rm -rf' },
          { type: 'tool_result', tool_use_id: 't', content: 7 },
          { type: 'image', source: 'base64…' },
          { type: 'text', text: 'ok', truncated: 'yes', total_bytes: -3 },
        ] } },
      }],
    })
    const it0 = page!.items[0]
    if (it0.kind !== 'assistant') throw new Error('kind')
    expect((it0.msg as { message: { content: unknown[] } }).message.content).toEqual([
      { type: 'text' },
      { type: 'tool_use', id: 't', name: 'Bash', input: {} },
      { type: 'tool_result', tool_use_id: 't', content: '' },
      { type: 'text', text: 'ok' },
    ])
  })
})

const contentOf = (page: ReturnType<typeof sanitizePreludePage>): unknown[] => {
  const it0 = page!.items[0]
  if (it0.kind !== 'assistant' && it0.kind !== 'user') throw new Error('kind')
  return (it0.msg as unknown as { message: { content: unknown[] } }).message.content
}
const blocksPage = (content: unknown[]) => sanitizePreludePage({
  state: 'ok', prev_cursor: null,
  items: [{ pos: '1', kind: 'assistant', at: 1, payload: { type: 'assistant', message: { role: 'assistant', content } } }],
})

describe('closed per-type block cleaning (spec §5.2)', () => {
  it('rebuilds tool_use from known fields only', () => {
    expect(contentOf(blocksPage([{ type: 'tool_use', id: 5, name: { x: 1 }, input: [1], extra: 'x' }]))).toEqual([{ type: 'tool_use', input: {} }])
    expect(contentOf(blocksPage([{ type: 'tool_use', id: 't', name: 'Bash', input: { a: 1 } }]))).toEqual([{ type: 'tool_use', id: 't', name: 'Bash', input: { a: 1 } }])
  })

  it('rebuilds tool_result, cleaning nested content with the nested rule', () => {
    expect(contentOf(blocksPage([{
      type: 'tool_result', tool_use_id: { a: 1 }, is_error: 'yes',
      content: [{ type: 'text', text: 5 }, 'x', { type: 'image', source: { type: 'omitted', media_type: {}, bytes: '9' } }],
    }]))).toEqual([{
      type: 'tool_result', content: [{ type: 'text' }, { type: 'image', source: { type: 'omitted' } }],
    }])
    expect(contentOf(blocksPage([{ type: 'tool_result', tool_use_id: 't', is_error: true, content: 'out' }])))
      .toEqual([{ type: 'tool_result', tool_use_id: 't', is_error: true, content: 'out' }])
  })

  it('rebuilds image sources and drops an image whose source.type is not a string', () => {
    expect(contentOf(blocksPage([{ type: 'image', source: { type: 3 } }, { type: 'image', source: { type: 'omitted', media_type: 'image/png', bytes: 48213, junk: 1 } }])))
      .toEqual([{ type: 'image', source: { type: 'omitted', media_type: 'image/png', bytes: 48213 } }])
  })

  it('keeps only {type} for an unknown type, and drops unknown fields on a text block', () => {
    expect(contentOf(blocksPage([{ type: 'weird', a: 1, text: 'x' }, { type: 'text', text: 'ok', extra: 1 }])))
      .toEqual([{ type: 'weird' }, { type: 'text', text: 'ok' }])
  })
})

describe('an ok page must carry an items array (spec §4.2)', () => {
  it.each([[undefined], [null], [{}], ['x']])('items %j on an ok page is not a page', (items) => {
    expect(sanitizePreludePage({ state: 'ok', ...(items === undefined ? {} : { items }), prev_cursor: null })).toBeNull()
  })
  it('none / gone still ignore items', () => {
    expect(sanitizePreludePage({ state: 'none', items: 'x', prev_cursor: null })).not.toBeNull()
    expect(sanitizePreludePage({ state: 'gone', prev_cursor: null })).not.toBeNull()
  })
})

describe('client resource budget (spec §4.3)', () => {
  const note = (text: string) => ({ pos: '1', kind: 'prelude.note', at: 0, payload: { source: 'command_output', text } })

  it('a page over the item budget is not a page', () => {
    const items = new Array(PRELUDE_MAX_PAGE_ITEMS + 1).fill(null)
    expect(sanitizePreludePage({ state: 'ok', items, prev_cursor: null })).toBeNull()
    expect(sanitizePreludePage({ state: 'ok', items: items.slice(1), prev_cursor: null })).not.toBeNull()
  })

  it('a string over 4 MiB aborts the page; one byte under passes', () => {
    expect(sanitizePreludePage({ state: 'ok', items: [note('a'.repeat(PRELUDE_MAX_STRING_BYTES + 1))], prev_cursor: null })).toBeNull()
    expect(sanitizePreludePage({ state: 'ok', items: [note('a'.repeat(PRELUDE_MAX_STRING_BYTES - 1))], prev_cursor: null })).not.toBeNull()
  })

  it('counts UTF-8 bytes, not code units, and covers block text and tool_result content', () => {
    // 3-byte chars: 1.4M of them is under 4M in length but over in bytes.
    expect(sanitizePreludePage({ state: 'ok', items: [note('€'.repeat(1_500_000))], prev_cursor: null })).toBeNull()
    expect(blocksPage([{ type: 'text', text: 'a'.repeat(PRELUDE_MAX_STRING_BYTES + 1) }])).toBeNull()
    expect(blocksPage([{ type: 'tool_result', content: 'a'.repeat(PRELUDE_MAX_STRING_BYTES + 1) }])).toBeNull()
    expect(blocksPage([{ type: 'tool_result', content: [{ type: 'text', text: 'a'.repeat(PRELUDE_MAX_STRING_BYTES + 1) }] }])).toBeNull()
  })
})

describe('contract sample (spec §4.3)', () => {
  it('every kind of the hand-written sample page survives the sanitiser unchanged', () => {
    const page = sanitizePreludePage(sample)!
    expect(page.state).toBe('ok')
    expect(page.items).toHaveLength(sample.items.length)
    expect(new Set(page.items.map((i) => i.kind))).toEqual(new Set(['prelude.segment', 'user', 'assistant', 'tool_use', 'tool_result', 'prelude.note', 'prelude.compaction']))
    const img = page.items.flatMap((i) => (i.kind === 'user' ? [i.msg as unknown as { message: { content: Array<{ type: string }> } }] : []))
      .flatMap((m) => m.message.content).find((b) => b.type === 'image')
    expect(img).toMatchObject({ source: { type: 'omitted', media_type: 'image/png', bytes: 48213 } })
    const res = page.items.find((i) => i.kind === 'tool_result')
    expect(res).toMatchObject({ payload: { output: { text: 'README.md\nspa\n', total_lines: 2, total_bytes: 14, truncated: false } } })
  })
})
