import { describe, it, expect, beforeEach, vi } from 'vitest'
import { wireEntry } from './fixtures'
import { parseConversation, parseEntries, parseEntry, parseEntryEvent, parseStatusEvent, resetWarned } from './parse'

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
