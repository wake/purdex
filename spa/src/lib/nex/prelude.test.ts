// spa/src/lib/nex/prelude.test.ts
import { describe, it, expect } from 'vitest'
import { sanitizePreludePage } from './prelude-wire'
import golden from './__fixtures__/prelude-golden-nexen.json'
import { applyPreludePage, defaultPreludeState, derivePrelude, preludeBlocks, preludeFailed, preludeLoading, type PreludeState } from './prelude'
import type { PreludeItem, PreludePage } from './prelude-wire'
import type { ContentBlock, StreamMessage } from './message-types'
import { isOpeningLine } from './turns'

const msg = (pos: string, type: 'user' | 'assistant', content: unknown[]): PreludeItem =>
  ({ offset: null, pos, at: 1000, kind: type, msg: { type, parent_tool_use_id: null, message: { role: type, content, stop_reason: null } } as unknown as StreamMessage })
const ok = (items: PreludeItem[], prevCursor: string | null): PreludePage => ({ state: 'ok', items, prevCursor, totalBytes: null })

/** Load one page as request `r` — the way the hook does it. */
const load = (p: PreludeState, page: PreludePage, sentBefore: string | null, r: number) =>
  applyPreludePage(preludeLoading(p, r), page, sentBefore, r)

describe('applyPreludePage', () => {
  it('prepends an older page and moves the cursor', () => {
    let p = load(defaultPreludeState(), ok([msg('20', 'user', [])], 'c1'), null, 1)
    expect(p).toMatchObject({ status: 'ok', cursor: 'c1', done: false, request: null })
    p = load(p, ok([msg('10', 'user', [])], null), 'c1', 2)
    expect(p.items.map((i) => i.pos)).toEqual(['10', '20'])
    expect(p).toMatchObject({ status: 'ok', cursor: null, done: true })
  })

  it('dedupes by pos', () => {
    const p1 = load(defaultPreludeState(), ok([msg('10', 'user', []), msg('20', 'user', [])], 'c1'), null, 1)
    const p2 = load(p1, ok([msg('5', 'user', []), msg('10', 'user', [])], null), 'c1', 2)
    expect(p2.items.map((i) => i.pos)).toEqual(['5', '10', '20'])
  })

  it('only the recorded request lands (Review Focus 3)', () => {
    const loading = preludeLoading(defaultPreludeState(), 2)
    expect(applyPreludePage(loading, ok([msg('10', 'user', [])], null), null, 1)).toBe(loading)
    expect(preludeFailed(loading, 'late', 1)).toBe(loading)
    expect(applyPreludePage(defaultPreludeState(), ok([], null), null, 1)).toEqual(defaultPreludeState())
  })

  it('stuck cursor: a prev_cursor equal to the before it answered is an error, not a loop', () => {
    const p1 = load(defaultPreludeState(), ok([msg('10', 'user', [])], 'c1'), null, 1)
    const p2 = load(p1, ok([], 'c1'), 'c1', 2)
    expect(p2.status).toBe('error')
    expect(p2.items).toBe(p1.items)
    expect(p2.cursor).toBe('c1')
  })

  it('a cursor cycle c1 -> c2 -> c1 errors on the third page and keeps the items; seenCursors grows', () => {
    const p1 = load(defaultPreludeState(), ok([msg('30', 'user', [])], 'c1'), null, 1)
    expect(p1.seenCursors).toEqual(['c1'])
    const p2 = load(p1, ok([msg('20', 'user', [])], 'c2'), 'c1', 2)
    expect(p2.seenCursors).toEqual(['c1', 'c2'])
    const p3 = load(p2, ok([msg('10', 'user', [])], 'c1'), 'c2', 3)
    expect(p3).toMatchObject({ status: 'error', error: 'prelude cursor did not advance', request: null, cursor: 'c2' })
    expect(p3.items).toBe(p2.items)
    expect(p3.seenCursors).toEqual(['c1', 'c2'])
  })

  it('gone ends the prelude and keeps what was loaded; none on an older page is an error', () => {
    const p1 = load(defaultPreludeState(), ok([msg('10', 'user', [])], 'c1'), null, 1)
    const g = load(p1, { state: 'gone', items: [], prevCursor: null, totalBytes: null }, 'c1', 2)
    expect(g).toMatchObject({ status: 'gone', done: true, cursor: null })
    expect(g.items).toHaveLength(1)
    const n = load(p1, { state: 'none', items: [], prevCursor: null, totalBytes: null }, 'c1', 3)
    expect(n).toMatchObject({ status: 'error', cursor: 'c1' })
    expect(load(defaultPreludeState(), { state: 'none', items: [], prevCursor: null, totalBytes: null }, null, 4))
      .toMatchObject({ status: 'none', done: true })
  })

  it('preludeFailed keeps items and cursor', () => {
    const p1 = load(defaultPreludeState(), ok([msg('10', 'user', [])], 'c1'), null, 1)
    expect(preludeFailed(preludeLoading(p1, 2), 'boom', 2)).toMatchObject({ status: 'error', error: 'boom', cursor: 'c1', items: p1.items, request: null })
  })
})

describe('derivePrelude', () => {
  it('lists messages and markers in order, with stable ids', () => {
    const v = derivePrelude([
      { offset: null, pos: '1', at: 0, kind: 'prelude.segment', entrypoint: 'cli' },
      msg('2', 'user', [{ type: 'text', text: 'hi' }]),
      { offset: null, pos: '3', at: 0, kind: 'prelude.note', source: 'command_output', text: 'out', truncated: false, totalBytes: null, stream: null },
      msg('4', 'assistant', [{ type: 'text', text: 'yo' }]),
    ])
    expect(v.entries.map((e) => e.kind)).toEqual(['segment', 'message', 'note', 'message'])
    expect(v.ids).toEqual(['p2', 'p4'])
    expect(v.messages).toHaveLength(2)
    expect(v.entries[3]).toMatchObject({ kind: 'message', m: 1 })
  })

  it('builds the tool overlay from N2 items and closes a call that was never answered', () => {
    const v = derivePrelude([
      msg('1', 'assistant', [{ type: 'tool_use', id: 'toolu_a', name: 'Bash', input: {} }]),
      { offset: null, pos: '1.1', at: 1000, kind: 'tool_use', payload: { tool_use_id: 'toolu_a', name: 'Bash' } },
      msg('2', 'assistant', [{ type: 'tool_use', id: 'toolu_b', name: 'Read', input: {} }]),
      { offset: null, pos: '2.1', at: 2000, kind: 'tool_use', payload: { tool_use_id: 'toolu_b', name: 'Read' } },
      msg('3', 'user', [{ type: 'tool_result', tool_use_id: 'toolu_a', content: 'x' }]),
      { offset: null, pos: '3.1', at: 3000, kind: 'tool_result', payload: { tool_use_id: 'toolu_a', status: 'ok', duration_ms: 2000 } },
    ])
    expect(v.tools.toolu_a.status).toBe('done')
    expect(v.tools.toolu_b.status).toBe('aborted')
  })

  it('a __proto__ tool id is an own key, never the prototype', () => {
    const v = derivePrelude([
      { offset: null, pos: '1', at: 1, kind: 'tool_use', payload: { tool_use_id: '__proto__', name: 'X' } },
      { offset: null, pos: '2', at: 2, kind: 'tool_result', payload: { tool_use_id: '__proto__', status: 'ok' } },
    ])
    expect(Object.hasOwn(v.tools, '__proto__')).toBe(true)
    expect(Object.getOwnPropertyDescriptor(v.tools, '__proto__')?.value.status).toBe('done')
    expect(Object.getPrototypeOf(v.tools)).toBe(Object.prototype)
  })
})

describe('preludeBlocks', () => {
  it('cuts spans at opening lines and around non-message entries', () => {
    const v = derivePrelude([
      { offset: null, pos: '1', at: 0, kind: 'prelude.segment', entrypoint: 'cli' },
      msg('2', 'user', [{ type: 'text', text: 'one' }]),
      msg('3', 'assistant', [{ type: 'text', text: 'a' }]),
      msg('4', 'user', [{ type: 'text', text: 'two' }]),
      { offset: null, pos: '5', at: 0, kind: 'prelude.note', source: 'task_notification', text: 'n', truncated: false, totalBytes: null, stream: null },
      msg('6', 'assistant', [{ type: 'text', text: 'b' }]),
    ])
    expect(preludeBlocks(v)).toEqual([
      { kind: 'entry', entry: v.entries[0] },
      { kind: 'span', start: 0, end: 2 },
      { kind: 'span', start: 2, end: 3 },
      { kind: 'entry', entry: v.entries[4] },
      { kind: 'span', start: 3, end: 4 },
    ])
  })
})

describe('derivePrelude — pasted text (U3)', () => {
  const PASTE = '<pasted_content id="bb1b">\nline 1\nline 2\n</pasted_content id="bb1b">'
  const content = (v: ReturnType<typeof derivePrelude>, m: number) => (v.messages[m] as { message: { content: unknown[] } }).message.content

  it('splits a human user text block into typed parts and pasted bodies', () => {
    const v = derivePrelude([msg('1', 'user', [{ type: 'text', text: `look:\n${PASTE}` }, { type: 'image', source: { type: 'omitted', media_type: 'image/png', bytes: 3 } }])])
    expect(content(v, 0)).toEqual([
      { type: 'text', text: 'look:' },
      { type: 'text', text: 'line 1\nline 2', pasted: { lines: 2, cut: false } },
      { type: 'image', source: { type: 'omitted', media_type: 'image/png', bytes: 3 } },
    ])
  })

  it('leaves a tool_result carrier, a subagent frame and assistant text untouched', () => {
    const carrier = msg('1', 'user', [{ type: 'tool_result', tool_use_id: 't', content: PASTE }, { type: 'text', text: PASTE }])
    const frame: PreludeItem = { pos: '2', at: 0, offset: null, kind: 'user', msg: { type: 'user', parent_tool_use_id: 'task', message: { role: 'user', content: [{ type: 'text', text: PASTE }], stop_reason: null } } as unknown as StreamMessage }
    const agent = msg('3', 'assistant', [{ type: 'text', text: PASTE }])
    const v = derivePrelude([carrier, frame, agent])
    expect(v.messages[0]).toBe((carrier as { msg: StreamMessage }).msg)
    expect(v.messages[1]).toBe(frame.msg)
    expect(v.messages[2]).toBe((agent as { msg: StreamMessage }).msg)
  })

  it('keeps a message with nothing to split as the same object', () => {
    const plain = msg('1', 'user', [{ type: 'text', text: 'hi' }])
    expect(derivePrelude([plain]).messages[0]).toBe((plain as { msg: StreamMessage }).msg)
  })

  describe('wire path, the live capture\'s shape (execution 06GGS9V6…)', () => {
    // The CLI's wrapper (≤ 2 newlines before the opener and after the closer
    // are its own) around 1501 filler lines of 50 chars; 76617 is the capture's total.
    const lines = Array.from({ length: 1501 }, (_, k) => `filler line ${String(k).padStart(5, '0')} for the prelude truncation check`)
    const full = `\n\n<pasted_content id="bb1b">\n${lines.join('\n')}\n</pasted_content id="bb1b">\n`
    const through = (block: Record<string, unknown>) => content(derivePrelude(sanitizePreludePage({
      state: 'ok', prev_cursor: null,
      items: [{ pos: '9', kind: 'user', at: 1, payload: { type: 'user', message: { role: 'user', content: [block] } } }],
    })!.items), 0) as ContentBlock[]

    it('complete: a single pasted block of N lines, not cut, no typed parts', () => {
      expect(through({ type: 'text', text: full })).toEqual([{ type: 'text', text: lines.join('\n'), pasted: { lines: 1501, cut: false } }])
    })

    it('cut at 64 KiB (closer gone): a cut paste carrying the hint flags and the whole block\'s shown size', () => {
      const shown = full.slice(0, 65536)
      const [pasted, ...rest] = through({ type: 'text', text: shown, truncated: true, total_bytes: 76617 })
      expect(rest).toEqual([])
      // 65536 − 29 wrapper chars before the body = 1284 whole 51-char lines + 23 chars.
      expect(pasted).toMatchObject({ type: 'text', pasted: { lines: 1285, cut: true }, truncated: true, total_bytes: 76617, shown_bytes: 65536 })
      expect(pasted.text!.startsWith('filler line 00000 ')).toBe(true)
      expect(pasted.text!.endsWith('\nfiller line 01284 for t')).toBe(true)
    })
  })
})

describe('preludeBlocks — a pasted-only prompt (U3)', () => {
  it('opens a new chat span; the agent\'s work before it stays in the previous span', () => {
    const v = derivePrelude([
      msg('1', 'user', [{ type: 'text', text: 'q1' }]),
      msg('2', 'assistant', [{ type: 'tool_use', id: 't', name: 'Bash', input: {} }]),
      msg('3', 'user', [{ type: 'tool_result', tool_use_id: 't', content: 'out' }]),
      msg('4', 'assistant', [{ type: 'text', text: 'a1' }]),
      msg('5', 'user', [{ type: 'text', text: '<pasted_content id="0a0a">\nonly pasted\n</pasted_content id="0a0a">' }]),
      msg('6', 'assistant', [{ type: 'text', text: 'a2' }]),
    ])
    expect(isOpeningLine(v.messages[4])).toBe(true)
    expect(preludeBlocks(v)).toEqual([{ kind: 'span', start: 0, end: 4 }, { kind: 'span', start: 4, end: 6 }])
  })
})

describe('Nexen golden page (spec §4.6)', () => {
  const page = sanitizePreludePage(golden)!
  const view = derivePrelude(page.items)

  it('pairs every tool_use with its result, denied included — and carries the N2 facts the SPA reads', () => {
    expect(Object.keys(view.tools).sort()).toEqual(['toolu_golden0001', 'toolu_golden0002', 'toolu_golden0003', 'toolu_golden0004'])
    // Values read off the fixture's N2 items (tool_use 6688.2 / 16045.2 / 111120.2 / 31886.2 and their
    // results 8399.2 / 17694.2 / 202834.2 / 33612.2): at → startedAt / endedAt, primary_arg, known,
    // duration_ms, output counts. The N2 `output.text` is deliberately not kept (the user block has it).
    expect(view.tools).toEqual({
      toolu_golden0001: {
        name: 'Bash', status: 'done', startedAt: 1790812804123, endedAt: 1790812805123,
        primaryArg: { key: 'command', value: 'df -h /' }, known: true, durationMs: 1000,
        output: { totalLines: 1, totalBytes: 200, truncated: false, hasNonText: false },
      },
      toolu_golden0002: {
        name: 'Read', status: 'done', startedAt: 1790812815123, endedAt: 1790812816123,
        primaryArg: { key: 'file_path', value: '/Users/dev/golden/shot.png' }, known: true, durationMs: 1000,
        output: { totalLines: 0, totalBytes: 0, truncated: false, hasNonText: true },
      },
      toolu_golden0003: {
        name: 'Write', status: 'done', startedAt: 1790812833123, endedAt: 1790812834123,
        primaryArg: { key: 'file_path', value: '/Users/dev/golden/report.md' }, known: true, durationMs: 1000,
        output: { totalLines: 1, totalBytes: 80000, truncated: true, hasNonText: false },
      },
      toolu_golden0004: {
        name: 'Edit', status: 'denied', startedAt: 1790812823123, endedAt: 1790812824123,
        primaryArg: { key: 'file_path', value: '/Users/dev/golden/a.txt' }, known: true, durationMs: 1000,
        output: { totalLines: 1, totalBytes: 52, truncated: false, hasNonText: false },
      },
    })
  })

  it('loses no item: every message, note, segment and compaction shows up once, in order', () => {
    const shown = page.items.filter((i) => i.kind !== 'tool_use' && i.kind !== 'tool_result')
    expect(view.messages).toHaveLength(22)
    expect(view.entries.map((e) => e.pos)).toEqual(shown.map((i) => i.pos))
    expect(view.ids).toEqual(page.items.filter((i) => i.kind === 'user' || i.kind === 'assistant').map((i) => `p${i.pos}`))
    const kinds = (k: string) => view.entries.filter((e) => e.kind === k).length
    expect([kinds('message'), kinds('note'), kinds('segment'), kinds('compaction')]).toEqual([22, 9, 3, 1])
  })

  it('preludeBlocks covers every entry exactly once without throwing', () => {
    const blocks = preludeBlocks(view)
    const covered = blocks.flatMap((b) => (b.kind === 'entry' ? [b.entry.pos] : view.entries.filter((e) => e.kind === 'message' && e.m >= b.start && e.m < b.end).map((e) => e.pos)))
    expect(covered).toEqual(view.entries.map((e) => e.pos))
    expect(blocks.filter((b) => b.kind === 'entry').length).toBe(13)
    expect(blocks.some((b) => b.kind === 'span')).toBe(true)
  })
})
