// spa/src/stores/useJournalStore.sessions.test.ts — session pages in the journal store (JV-4, journal.sessions.v1).
import { beforeEach, describe, expect, it, vi } from 'vitest'

const getJournal = vi.fn()
const getJournalPage = vi.fn()
const getJournalSessions = vi.fn()
vi.mock('../lib/journal/api', () => ({
  getJournal: (...a: unknown[]) => getJournal(...a),
  getJournalPage: (...a: unknown[]) => getJournalPage(...a),
  getJournalSessions: (...a: unknown[]) => getJournalSessions(...a),
  getTasks: vi.fn(), getRoadmap: vi.fn(), setConfirmItem: vi.fn().mockResolvedValue({ kind: 'ok', value: true }),
}))

import { selectUnit, sessionKey, useJournalStore } from './useJournalStore'
import { parseJournalPage, parseSessions } from '../lib/journal/parse'
import { clearViewMemos } from '../lib/journal/view-memory'
import { T0, wireConfirm, wireEntry, wirePage } from '../lib/journal/fixtures'

const ok = (value: unknown) => ({ kind: 'ok', value })
const pageOf = (over: Record<string, unknown> = {}) => ok(parseJournalPage(wirePage(over)))
const sessionsOf = (ids: string[], incomplete = false) => ok(parseSessions({
  sessions: ids.map((id, i) => ({ session_id: id, started_at: 100 * (i + 1), ended_at: i + 1 < ids.length ? 100 * (i + 2) : 0, via: i === 0 ? 'root' : 'relay', current: i === ids.length - 1 })),
  ...(incomplete ? { incomplete: true } : {}),
}))
const st = () => useJournalStore.getState()
const unit = () => selectUnit(st(), 'h1', 's1')!
const ids = () => unit().entries.map((e) => e.id)

beforeEach(() => {
  st().reset()
  clearViewMemos()
  getJournal.mockReset(); getJournalPage.mockReset(); getJournalSessions.mockReset()
  getJournalSessions.mockResolvedValue(sessionsOf(['old', 'cur']))
  st().setSupport('h1', true, true, true)
})

describe('support', () => {
  it('journal.sessions.v1 is its own live answer, cleared by a fence', () => {
    expect(st().sessionsSupport.h1).toBe(true)
    st().fence('h1')
    expect(st().sessionsSupport.h1).toBeUndefined()
    st().setSupport('h1', true, true)
    expect(st().sessionsSupport.h1).toBe(false)
  })
})

describe('reading the current session', () => {
  it('asks for a session page (never the before-paging read), keeps the page id and truncated, and has no older months', async () => {
    getJournalPage.mockResolvedValue(pageOf({ page: 'cur', truncated: true, more: true }))
    await st().loadJournal('h1', 's1')
    expect(getJournal).not.toHaveBeenCalled()
    expect(getJournalPage).toHaveBeenCalledWith('h1', 's1')
    expect(getJournalPage.mock.calls[0]).toHaveLength(2) // no page argument: the current session
    expect(unit().currentPage).toBe('cur')
    expect(unit().truncated).toBe(true)
    expect(unit().more).toBe(false)
  })
  it('a page without truncated is not truncated', async () => {
    getJournalPage.mockResolvedValue(pageOf({ page: 'cur' }))
    await st().loadJournal('h1', 's1')
    expect(unit().truncated).toBe(false)
  })
  it('reads the chain of sessions after the page', async () => {
    getJournalPage.mockResolvedValue(pageOf({ page: 'cur' }))
    getJournalSessions.mockResolvedValue(sessionsOf(['old', 'cur'], true))
    await st().loadJournal('h1', 's1')
    expect(getJournalSessions).toHaveBeenCalledWith('h1', 's1')
    expect(unit().sessions?.map((s) => s.sessionId)).toEqual(['old', 'cur'])
    expect(unit().sessionsIncomplete).toBe(true)
  })
  it('a failed chain read does not fail the page', async () => {
    getJournalPage.mockResolvedValue(pageOf({ page: 'cur' }))
    getJournalSessions.mockRejectedValue(new Error('503'))
    expect((await st().loadJournal('h1', 's1')).kind).toBe('ok')
    expect(unit().sessions).toBeNull()
  })
  it('a new current session (a relay) replaces the entries instead of merging them in', async () => {
    getJournalPage.mockResolvedValueOnce(pageOf({ page: 'cur', entries: [wireEntry('a', { at: T0 })] }))
    await st().loadJournal('h1', 's1')
    getJournalPage.mockResolvedValueOnce(pageOf({ page: 'next', entries: [wireEntry('b', { at: T0 + 5 })] }))
    await st().loadJournal('h1', 's1')
    expect(ids()).toEqual(['b'])
    expect(unit().currentPage).toBe('next')
  })
  it('the same session merges: a live event adds to the page', async () => {
    getJournalPage.mockResolvedValueOnce(pageOf({ page: 'cur', entries: [wireEntry('a', { at: T0 })] }))
    await st().loadJournal('h1', 's1')
    getJournalPage.mockResolvedValueOnce(pageOf({ page: 'cur', entries: [wireEntry('a', { at: T0 }), wireEntry('b', { at: T0 + 5 })] }))
    await st().loadJournal('h1', 's1')
    expect(ids()).toEqual(['a', 'b'])
  })
  it('without the capability the old before + limit read is used and nothing about sessions is asked', async () => {
    st().fence('h1')
    st().setSupport('h1', true, true, false)
    getJournal.mockResolvedValue(pageOf({ more: true }))
    await st().loadJournal('h1', 's1')
    expect(getJournal).toHaveBeenCalledTimes(1)
    expect(getJournalPage).not.toHaveBeenCalled()
    expect(getJournalSessions).not.toHaveBeenCalled()
    expect(unit().more).toBe(true)
    expect(unit().currentPage).toBe('')
    getJournal.mockResolvedValue(pageOf({ entries: [wireEntry('old1', { at: T0 - 9 })], more: false }))
    await st().loadJournal('h1', 's1', { older: true })
    expect(getJournal.mock.calls[1][2]).toMatch(/:/) // the before cursor
  })
  it('a failed read (the daemon\'s 503) is remembered as failed and a good one clears it', async () => {
    getJournalPage.mockRejectedValueOnce(new Error('503'))
    expect((await st().loadJournal('h1', 's1')).kind).toBe('failed')
    expect(st().readFailed[sessionKey('h1', 's1')]).toBe(true)
    getJournalPage.mockResolvedValueOnce(pageOf({ page: 'cur' }))
    await st().loadJournal('h1', 's1')
    expect(st().readFailed[sessionKey('h1', 's1')]).toBeUndefined()
  })
})

describe('reading another session', () => {
  beforeEach(async () => {
    getJournalPage.mockResolvedValue(pageOf({
      page: 'cur', entries: [wireEntry('cur1', { at: T0 + 50 })], confirms: [wireConfirm('c-open')], open_confirms: 1,
    }))
    await st().loadJournal('h1', 's1')
  })
  it('asks with only the page and keeps the rows apart from the current page', async () => {
    getJournalPage.mockResolvedValue(pageOf({ page: 'old', entries: [wireEntry('old1', { at: T0 }), wireEntry('old0', { at: T0 - 1 })], truncated: true }))
    expect((await st().loadPage('h1', 's1', 'old')).kind).toBe('ok')
    expect(getJournalPage).toHaveBeenLastCalledWith('h1', 's1', 'old')
    expect(unit().pages.old.entries.map((e) => e.id)).toEqual(['old0', 'old1'])
    expect(unit().pages.old.truncated).toBe(true)
    expect(ids()).toEqual(['cur1'])
  })
  it('does not touch the 待確認 band or its count (they are the whole unit\'s and do not change with the page)', async () => {
    getJournalPage.mockResolvedValue(pageOf({ page: 'old', entries: [wireEntry('old1')], confirms: [], open_confirms: 0 }))
    await st().loadPage('h1', 's1', 'old')
    expect(unit().confirms.map((e) => e.id)).toEqual(['c-open'])
    expect(unit().openConfirms).toBe(1)
  })
  it('a failed read (503) is a failed result and writes nothing', async () => {
    getJournalPage.mockRejectedValue(new Error('503'))
    expect((await st().loadPage('h1', 's1', 'old')).kind).toBe('failed')
    expect(unit().pages.old).toBeUndefined()
  })
  it('a page the daemon does not know (404) is none and writes nothing', async () => {
    getJournalPage.mockResolvedValue({ kind: 'none' })
    expect((await st().loadPage('h1', 's1', 'ghost')).kind).toBe('none')
    expect(unit().pages.ghost).toBeUndefined()
  })
  it('a hand check on a row of an older page shows on that page', async () => {
    getJournalPage.mockResolvedValue(pageOf({ page: 'old', entries: [wireConfirm('old-c', { items: [{ text: 'q', done: false }] })] }))
    await st().loadPage('h1', 's1', 'old')
    await st().setConfirm('h1', 's1', 'old-c', 0, true)
    expect(unit().pages.old.entries[0].items[0].done).toBe(true)
  })
})
