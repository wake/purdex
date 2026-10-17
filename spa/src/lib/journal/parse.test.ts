// spa/src/lib/journal/parse.test.ts — the strict parse of the journal wire (J-2a T-2a.2).
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { parseChangedEvent, parseEntry, parseJournalPage, parseRemoteLead, parseSessions, parseRoadmap, parseTasks, resetWarned } from './parse'
import { ENTRY_KINDS } from './types'
import { T0, wireConfirm, wireEntry, wirePage, wireRoadmap, wireTasks } from './fixtures'

beforeEach(() => { resetWarned(); vi.restoreAllMocks() })

describe('parseEntry', () => {
  it.each(ENTRY_KINDS)('reads a %s entry', (kind) => {
    const e = parseEntry(wireEntry('wb:1:1', { kind, by: 'leader', refs: ['#1', 'PR #2'] }))
    expect(e).toMatchObject({ id: 'wb:1:1', kind, by: 'leader', at: T0, actor: 'Adam', refs: ['#1', 'PR #2'], items: [], detail: '' })
  })
  it('reads a confirm with its items and detail', () => {
    expect(parseEntry(wireConfirm('wb:2:1'))).toMatchObject({ kind: 'confirm', detail: 'the original question', items: [{ text: 'git?', done: true }, { text: 'tasks?', done: false }] })
  })
  it.each([
    ['not an object', 'x'],
    ['empty id', wireEntry('')],
    ['numeric id', { ...wireEntry('a'), id: 4 }],
    ['unknown kind', wireEntry('a', { kind: 'mystery' })],
    ['at as a string', wireEntry('a', { at: '2026-10-14' })],
    ['negative at', wireEntry('a', { at: -1 })],
    ['text missing', (() => { const { text: _t, ...rest } = wireEntry('a'); return rest })()],
    ['refs not strings', wireEntry('a', { refs: [4] })],
    ['an item without done', wireEntry('a', { items: [{ text: 'q' }] })],
    ['detail as a number', wireEntry('a', { detail: 5 })],
  ])('rejects a row with %s', (_n, row) => {
    expect(typeof parseEntry(row)).toBe('string')
  })
})

describe('parseJournalPage', () => {
  it('reads the envelope', () => {
    const p = parseJournalPage(wirePage({ open_confirms: 2, more: true, pinned: [wireConfirm('wb:3:1')] }))
    expect(p).toMatchObject({ unit: { kind: 'team', id: 'tm1' }, project: { remoteUrl: 'https://github.com/wake/purdex' }, personName: 'Wake', more: true, openConfirms: 2 })
    expect(p?.entries).toHaveLength(2)
    expect(p?.pinned).toHaveLength(1)
    expect(p?.confirms).toBeNull() // an older daemon sends none
  })
  it('reads confirms[] when sent, and ignores a per-entry pinned flag', () => {
    const p = parseJournalPage(wirePage({ confirms: [wireConfirm('wb:4:1')], pinned: [wireConfirm('wb:4:1')] }))
    expect(p?.confirms).toHaveLength(1)
    expect(p?.confirms?.[0]).not.toHaveProperty('pinned')
  })
  it('a confirms[] that is not a list rejects the page', () => {
    expect(parseJournalPage(wirePage({ confirms: 'x' }))).toBeNull()
  })
  it('person_name and remote_url are optional', () => {
    const p = parseJournalPage(wirePage({ person_name: undefined, project: { name: 'x' } }))
    expect(p).toMatchObject({ personName: '', project: { remoteUrl: '' } })
  })
  it('drops an unknown-kind or malformed row with one warning and keeps the rest', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const p = parseJournalPage(wirePage({ entries: [wireEntry('a', { kind: 'mystery' }), wireEntry('b', { kind: 'mystery' }), wireEntry('c')] }))
    expect(p?.entries.map((e) => e.id)).toEqual(['c'])
    expect(warn).toHaveBeenCalledTimes(1)
  })
  it.each([
    ['not an object', []],
    ['no unit', wirePage({ unit: undefined })],
    ['unit kind unknown', wirePage({ unit: { kind: 'org', id: 'a', label: 'l' } })],
    ['project missing', wirePage({ project: undefined })],
    ['remote_url as a number', wirePage({ project: { remote_url: 5 } })],
    ['more missing', wirePage({ more: undefined })],
    ['open_confirms negative', wirePage({ open_confirms: -1 })],
    ['person_name as a number', wirePage({ person_name: 3 })],
    ['entries not a list', wirePage({ entries: {} })],
  ])('rejects an envelope with %s', (_n, v) => {
    expect(parseJournalPage(v)).toBeNull()
  })
})

describe('tasks, roadmap, events', () => {
  it('reads tasks (a numeric number becomes its string)', () => {
    expect(parseTasks(wireTasks())?.map((t) => [t.number, t.state])).toEqual([['7bcf08-12', 'open'], ['3', 'done'], ['4', 'dropped']])
  })
  it('drops a task with an unknown state; rejects a non-envelope', () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    expect(parseTasks({ tasks: [{ number: '1', title: 't', state: 'blocked' }] })).toEqual([])
    expect(parseTasks({})).toBeNull()
  })
  it('reads the roadmap and drops a stage with an unknown status', () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    expect(parseRoadmap(wireRoadmap())?.[0]?.stages.map((s) => s.status)).toEqual(['完成', '進行中'])
    expect(parseRoadmap({ features: [{ name: 'F', stages: [{ stage: 's', description: 'd', status: 'done' }] }] })?.[0]?.stages).toEqual([])
    expect(parseRoadmap([])).toBeNull()
  })
  it('reads a journal.changed frame and rejects a malformed one', () => {
    expect(parseChangedEvent({ unit: { kind: 'team', id: 'tm1' }, project: 'p', what: 'tasks' })).toEqual({ unitKind: 'team', unitId: 'tm1', what: 'tasks' })
    expect(typeof parseChangedEvent({ unit: { kind: 'team', id: 'tm1' }, what: 'everything' })).toBe('string')
    expect(typeof parseChangedEvent({ unit: 'tm1', what: 'tasks' })).toBe('string')
  })
  it('reads the 409 body only when both fields are non-empty strings', () => {
    expect(parseRemoteLead({ lead_host_id: 'h', lead_session_id: 's' })).toEqual({ leadHostId: 'h', leadSessionId: 's' })
    expect(parseRemoteLead({ lead_host_id: 'h' })).toBeNull()
    expect(parseRemoteLead({ lead_host_id: '', lead_session_id: 's' })).toBeNull()
    expect(parseRemoteLead(null)).toBeNull()
  })
})

describe('session page fields', () => {
  it('a page without page / truncated (both omitempty) reads as an empty page id and not truncated', () => {
    const p = parseJournalPage(wirePage())
    expect(p?.page).toBe('')
    expect(p?.truncated).toBe(false)
  })
  it.each([['page', 123], ['page', null], ['page', {}], ['truncated', 'true'], ['truncated', 1], ['truncated', null]])('a %s of the wrong type (%j) is a malformed envelope, not a default', (field, value) => {
    expect(parseJournalPage(wirePage({ [field]: value }))).toBeNull()
  })
  it('reads page and truncated when sent', () => {
    const p = parseJournalPage(wirePage({ page: 'sess-1', truncated: true }))
    expect(p?.page).toBe('sess-1')
    expect(p?.truncated).toBe(true)
  })
})

describe('parseSessions', () => {
  const s = (id: string, over: Record<string, unknown> = {}) => ({ session_id: id, started_at: 10, ended_at: 0, via: 'root', current: false, ...over })
  it('reads sessions oldest first, incomplete defaults to false', () => {
    const r = parseSessions({ sessions: [s('a', { ended_at: 20 }), s('b', { via: 'clear', current: true })] })
    expect(r?.incomplete).toBe(false)
    expect(r?.sessions).toEqual([
      { sessionId: 'a', startedAt: 10, endedAt: 20, via: 'root', current: false },
      { sessionId: 'b', startedAt: 10, endedAt: 0, via: 'clear', current: true },
    ])
  })
  it.each([['yes'], [1], [null], [{}]])('an incomplete of the wrong type (%j) is a malformed envelope', (value) => {
    expect(parseSessions({ sessions: [], incomplete: value })).toBeNull()
  })
  it('reads incomplete', () => {
    expect(parseSessions({ sessions: [], incomplete: true })?.incomplete).toBe(true)
  })
  it('drops a malformed row, and a malformed envelope is null', () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    expect(parseSessions({ sessions: [s(''), s('a', { via: 'weird' }), s('ok')] })?.sessions.map((x) => x.sessionId)).toEqual(['ok'])
    expect(parseSessions({ nope: 1 })).toBeNull()
  })
})
