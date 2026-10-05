// spa/src/lib/nex/prelude.test.ts
import { describe, it, expect } from 'vitest'
import { applyPreludePage, defaultPreludeState, derivePrelude, preludeFailed, preludeLoading, type PreludeState } from './prelude'
import type { PreludeItem, PreludePage } from './prelude-wire'
import type { StreamMessage } from './message-types'

const msg = (pos: string, type: 'user' | 'assistant', content: unknown[]): PreludeItem =>
  ({ pos, at: 1000, kind: type, msg: { type, parent_tool_use_id: null, message: { role: type, content, stop_reason: null } } as unknown as StreamMessage })
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
      { pos: '1', at: 0, kind: 'prelude.segment', entrypoint: 'cli' },
      msg('2', 'user', [{ type: 'text', text: 'hi' }]),
      { pos: '3', at: 0, kind: 'prelude.note', source: 'command_output', text: 'out', truncated: false, totalBytes: null, stream: null },
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
      { pos: '1.1', at: 1000, kind: 'tool_use', payload: { tool_use_id: 'toolu_a', name: 'Bash' } },
      msg('2', 'assistant', [{ type: 'tool_use', id: 'toolu_b', name: 'Read', input: {} }]),
      { pos: '2.1', at: 2000, kind: 'tool_use', payload: { tool_use_id: 'toolu_b', name: 'Read' } },
      msg('3', 'user', [{ type: 'tool_result', tool_use_id: 'toolu_a', content: 'x' }]),
      { pos: '3.1', at: 3000, kind: 'tool_result', payload: { tool_use_id: 'toolu_a', status: 'ok', duration_ms: 2000 } },
    ])
    expect(v.tools.toolu_a.status).toBe('done')
    expect(v.tools.toolu_b.status).toBe('aborted')
  })

  it('a __proto__ tool id is an own key, never the prototype', () => {
    const v = derivePrelude([
      { pos: '1', at: 1, kind: 'tool_use', payload: { tool_use_id: '__proto__', name: 'X' } },
      { pos: '2', at: 2, kind: 'tool_result', payload: { tool_use_id: '__proto__', status: 'ok' } },
    ])
    expect(Object.hasOwn(v.tools, '__proto__')).toBe(true)
    expect(Object.getOwnPropertyDescriptor(v.tools, '__proto__')?.value.status).toBe('done')
    expect(Object.getPrototypeOf(v.tools)).toBe(Object.prototype)
  })
})
