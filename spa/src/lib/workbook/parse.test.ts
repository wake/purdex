import { describe, it, expect, beforeEach, vi } from 'vitest'
import { wireEntry, wireTodo } from './fixtures'
import { parseConversation, parseEntries, parseEntry, parseEntryEvent, parseRefreshAvailableEvent, parseStatusEvent, parseTodo, parseTodos, parseTodosEvent, resetWarned } from './parse'

beforeEach(() => { resetWarned(); vi.restoreAllMocks() })

describe('parseEntry', () => {
  it('reads the times as ms and defaults the optional text', () => {
    const e = parseEntry(wireEntry({ turn_id: undefined, reason: undefined }))
    expect(e).toMatchObject({ id: 7, convKey: 'c1', sessionId: 's1', turnAt: 1_760_000_000_000, createdAt: 1_760_000_000_100, updatedAt: 1_760_000_000_200, turnId: '', reason: '' })
  })
  it.each([
    ['id', { id: 0 }], ['conv_key', { conv_key: '' }], ['state', { state: 'weird' }],
    ['time', { created_at: 'x' }], ['negative time', { updated_at: -1 }], ['thing type', { thing: 3 }], ['thing_done', { thing_done: 'yes' }],
  ])('rejects a bad %s', (_n, over) => {
    expect(typeof parseEntry(wireEntry(over))).toBe('string')
  })
  it('rejects a non-object', () => { expect(typeof parseEntry(null)).toBe('string') })
})

describe('parseEntries', () => {
  it('drops a malformed entry whole and warns once for the same malformation', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const out = parseEntries([wireEntry({ id: 1 }), wireEntry({ id: 2, state: 'x' }), wireEntry({ id: 3, state: 'y' }), wireEntry({ id: 4 })])
    expect(out.map((e) => e.id)).toEqual([1, 4])
    expect(warn).toHaveBeenCalledTimes(1)
  })
  it('reads a non-array as empty', () => { expect(parseEntries(undefined)).toEqual([]) })
})

describe('parseConversation', () => {
  it('reads the envelope and tolerates v2 parts it does not know', () => {
    const p = parseConversation({ conv_key: 'c1', status: 'doing', status_at: 5, entries: [wireEntry()], todos: { open: [], done: [] }, refresh_available: true })
    expect(p).toMatchObject({ convKey: 'c1', status: 'doing', statusAt: 5 })
    expect(p?.entries).toHaveLength(1)
  })
  it('defaults a missing status; rejects a missing conv_key', () => {
    expect(parseConversation({ conv_key: 'c1' })).toMatchObject({ status: '', statusAt: 0, entries: [] })
    expect(parseConversation({ status: 'x' })).toBeNull()
  })
})

describe('parseConversation envelope consistency', () => {
  it('rejects the whole answer when one entry belongs to another conversation, warning once', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const body = { conv_key: 'c1', entries: [wireEntry({ id: 1 }), wireEntry({ id: 2, conv_key: 'other' })] }
    expect(parseConversation(body)).toBeNull()
    expect(parseConversation(body)).toBeNull()
    expect(warn).toHaveBeenCalledTimes(1)
  })
})

describe('parseConversation, mismatch hidden behind a malformed entry', () => {
  it('rejects the whole answer although the mismatching entry is also malformed', () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    const body = { conv_key: 'c1', entries: [wireEntry({ id: 1 }), wireEntry({ id: 0, conv_key: 'other' })] }
    expect(parseConversation(body)).toBeNull()
  })
})

describe('v2 entry fields', () => {
  it('a v1 entry (no kind / usage / todo_changes) reads as a turn with zero usage and no changes', () => {
    expect(parseEntry(wireEntry())).toMatchObject({ kind: 'turn', usage: { in: 0, out: 0, cacheRead: 0 }, todoChanges: { added: [], done: [], dropped: [] } })
  })
  it('reads kind, usage and todo_changes', () => {
    const e = parseEntry(wireEntry({
      kind: 'refresh', usage: { in: 5, out: 6, cache_read: 7 },
      todo_changes: { added: [{ id: 1, title: 'a' }], done: [{ id: 2, title: 'b' }], dropped: null },
    }))
    expect(e).toMatchObject({ kind: 'refresh', usage: { in: 5, out: 6, cacheRead: 7 }, todoChanges: { added: [{ id: 1, title: 'a' }], done: [{ id: 2, title: 'b' }], dropped: [] } })
  })
  it.each([
    ['kind', { kind: 'weird' }], ['usage', { usage: 3 }], ['usage number', { usage: { in: -1 } }],
    ['todo_changes', { todo_changes: [] }], ['todo_changes item', { todo_changes: { added: [{ id: 'x', title: 'a' }] } }],
  ])('rejects a bad v2 %s', (_n, over) => { expect(typeof parseEntry(wireEntry(over))).toBe('string') })
})

describe('parseTodo / todo lists', () => {
  it('reads a todo and defaults the optional fields', () => {
    expect(parseTodo(wireTodo())).toMatchObject({ id: 1, title: 't', detail: 'd', state: 'open', closedAt: 0, addedEntryId: 3 })
    expect(parseTodo(wireTodo({ detail: undefined, closed_by: undefined, closed_at: undefined }))).toMatchObject({ detail: '', closedBy: '', closedAt: 0 })
  })
  it.each([['id', { id: 0 }], ['state', { state: 'x' }], ['title', { title: 3 }], ['time', { created_at: -1 }]])('rejects a bad %s', (_n, over) => {
    expect(typeof parseTodo(wireTodo(over))).toBe('string')
  })
  it('entry-id fields: 0 is "none" (the daemon sends closed_entry_id 0 while open); fractions, negatives, NaN and strings are malformed', () => {
    expect(parseTodo(wireTodo({ added_entry_id: 0, closed_entry_id: 0 }))).toMatchObject({ addedEntryId: 0, closedEntryId: 0 })
    for (const k of ['added_entry_id', 'closed_entry_id']) {
      for (const bad of [1.5, -1, NaN, Infinity, '3', 2 ** 60]) expect(typeof parseTodo(wireTodo({ [k]: bad }))).toBe('string')
    }
  })
  it('usage counts are non-negative safe integers', () => {
    for (const bad of [1.5, -1, NaN, '3', 2 ** 60]) expect(typeof parseEntry(wireEntry({ usage: { in: bad } }))).toBe('string')
    expect(typeof parseEntry(wireEntry({ usage: { cache_read: 0.5 } }))).toBe('string')
    expect(parseEntry(wireEntry({ usage: { in: 0, out: 3 } }))).toMatchObject({ usage: { in: 0, out: 3 } })
  })
  it('drops a malformed todo from a list and warns once', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    expect(parseTodos([wireTodo({ id: 1 }), wireTodo({ id: 2, state: 'x' }), wireTodo({ id: 3, state: 'y' })]).map((t) => t.id)).toEqual([1])
    expect(warn).toHaveBeenCalledTimes(1)
  })
})

describe('parseConversation v2', () => {
  it('a v1 answer has no todos and no refresh_available', () => {
    expect(parseConversation({ conv_key: 'c1', entries: [] })).toMatchObject({ todos: null, refreshAvailable: null })
  })
  it('reads todos {open, done} and refresh_available', () => {
    const p = parseConversation({ conv_key: 'c1', entries: [], todos: { open: [wireTodo({ id: 1 })], done: [wireTodo({ id: 2, state: 'done' })] }, refresh_available: true })
    expect(p?.todos?.open.map((t) => t.id)).toEqual([1])
    expect(p?.todos?.done.map((t) => t.id)).toEqual([2])
    expect(p?.refreshAvailable).toBe(true)
  })
  it('a malformed todos block or flag is ignored (null) with a warning, the answer stands', () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    expect(parseConversation({ conv_key: 'c1', entries: [], todos: 'x', refresh_available: 'yes' })).toMatchObject({ todos: null, refreshAvailable: null })
  })
})

describe('v2 events', () => {
  it('workbook.todos', () => {
    const v = JSON.stringify({ conv_key: 'c1', session_id: 's1', todos: [wireTodo({ id: 4, state: 'done' })] })
    expect(parseTodosEvent(v)).toMatchObject({ convKey: 'c1', sessionId: 's1', todos: [{ id: 4, state: 'done' }] })
    expect(typeof parseTodosEvent({ conv_key: 'c1', session_id: 's1', todos: 'x' })).toBe('string')
    expect(typeof parseTodosEvent({ conv_key: '', session_id: 's1', todos: [] })).toBe('string')
  })
  it('workbook.refresh_available', () => {
    expect(parseRefreshAvailableEvent(JSON.stringify({ conv_key: 'c1', available: true }))).toEqual({ convKey: 'c1', available: true })
    expect(typeof parseRefreshAvailableEvent({ conv_key: 'c1', available: 1 })).toBe('string')
  })
})

describe('events', () => {
  it('parses a workbook.entry value (JSON string)', () => {
    const ev = parseEntryEvent(JSON.stringify({ conv_key: 'c1', session_id: 's9', entry: wireEntry() }))
    expect(ev).toMatchObject({ convKey: 'c1', sessionId: 's9' })
  })
  it('rejects an entry event whose conv_key differs from the envelope or whose entry is bad', () => {
    expect(typeof parseEntryEvent({ conv_key: 'other', session_id: 's', entry: wireEntry() })).toBe('string')
    expect(typeof parseEntryEvent({ conv_key: 'c1', session_id: 's', entry: wireEntry({ id: 'x' }) })).toBe('string')
    expect(typeof parseEntryEvent('not json')).toBe('string')
  })
  it('parses a workbook.status value', () => {
    expect(parseStatusEvent({ conv_key: 'c1', session_id: 's1', status: 'hi', updated_at: 9 })).toEqual({ convKey: 'c1', sessionId: 's1', status: 'hi', updatedAt: 9 })
    expect(typeof parseStatusEvent({ conv_key: 'c1', status: 'hi', updated_at: 9 })).toBe('string')
  })
})
