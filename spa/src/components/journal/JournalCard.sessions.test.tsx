// spa/src/components/journal/JournalCard.sessions.test.tsx — the journal card with session pages (JV-4, journal.sessions.v1): it opens on the
// current session at the bottom, the header names the span, 上一頁 / 下一頁 move between the leader's sessions, filters and live entries stay
// on their page, the 待確認 band does not move, a 503 is shown as an error, and a host without the capability pages as before.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'

const getJournal = vi.fn()
const getJournalPage = vi.fn()
const getJournalSessions = vi.fn()
vi.mock('../../lib/journal/api', () => ({
  getJournal: (...a: unknown[]) => getJournal(...a),
  getJournalPage: (...a: unknown[]) => getJournalPage(...a),
  getJournalSessions: (...a: unknown[]) => getJournalSessions(...a),
  getTasks: vi.fn(),
  getRoadmap: vi.fn(),
  setConfirmItem: vi.fn().mockResolvedValue({ kind: 'ok', value: true }),
}))
vi.mock('../team/TeamSeatIcon', () => ({
  TeamSeatIcon: () => <span data-testid="seat-icon" />,
  TeamSeatHostBadge: () => <span data-testid="seat-host" />,
}))

import { CardStack } from './CardStack'
import { TeamDisplayProvider } from '../team/TeamDisplayProvider'
import { HOST, resetTeamStores, seedScene } from '../../lib/team/__tests__/team-fixture'
import { ccTabOf, seedUnit, setJournalHost, setSessionsSupport } from '../../lib/journal/__tests__/journal-fixture'
import { clearViewMemos, hasViewMemo, patchViewMemo, readViewMemo } from '../../lib/journal/view-memory'
import { parseJournalPage, parseSessions } from '../../lib/journal/parse'
import { wireEntry, wirePage } from '../../lib/journal/fixtures'
import { clearModuleRegistry } from '../../lib/module-registry'
import { useTabStore } from '../../stores/useTabStore'
import { selectUnit, sessionKey, useJournalStore } from '../../stores/useJournalStore'
import { useJournalUiStore } from '../../stores/useJournalUiStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useI18nStore } from '../../stores/useI18nStore'
import type { JournalEntry, SessionLink } from '../../lib/journal/types'

// jsdom has no layout: every divider / row is 20px tall, the stream's viewport 100px, and scrollTop clamps like a browser's.
const ROW = 20
const VIEW = 100
const scrollTops = new WeakMap<Element, number>()
const heightOf = (el: Element) => el.querySelectorAll('[data-testid="journal-entry"], [data-testid="journal-day"]').length * ROW
const maxTop = (el: Element) => Math.max(0, heightOf(el) - VIEW)

const D1 = new Date(2026, 9, 14, 9, 30).getTime()
const D2 = new Date(2026, 9, 15, 11, 45).getTime()
const entry = (id: string, at: number, over: Partial<JournalEntry> = {}): JournalEntry => ({
  id, kind: 'exec', by: '', at, actor: 'Adam', text: `text ${id}`, refs: [], items: [], detail: '', ...over,
})
const link = (sessionId: string, startedAt: number, endedAt: number, via: SessionLink['via'], current = false): SessionLink => ({ sessionId, startedAt, endedAt, via, current })
const S1 = link('s-1', D1, D2, 'root')
const S2 = link('s-2', D2, D2 + 3_600_000, 'relay')
const S3 = link('s-3', D2 + 3_600_000, 0, 'clear', true)
const SESSIONS = [S1, S2, S3]
const CUR = [entry('c1', D2 + 3_700_000), entry('c2', D2 + 3_800_000, { kind: 'task_new', text: '#9 current task' })]
const P2 = [entry('p2a', D2 + 10_000), entry('p2b', D2 + 20_000, { kind: 'task_new', text: '#5 middle task' })]
const P1 = [entry('p1a', D1 + 10_000)]
const BAND = entry('band1', D1 + 60_000, { kind: 'confirm', actor: 'Kenji → Wake', text: 'decide', items: [{ text: 'q', done: false }, { text: 'r', done: false }] })

const setActive = (id: string) => act(() => { useTabStore.setState({ activeTabId: id }) })
const mount = () => render(<TeamDisplayProvider><CardStack /></TeamDisplayProvider>)
const stream = () => screen.getByTestId('journal-stream')
const ids = () => within(stream()).queryAllByTestId('journal-entry').map((n) => n.getAttribute('data-entry-id'))
const prev = () => screen.getByTestId('journal-page-prev') as HTMLButtonElement
const next = () => screen.getByTestId('journal-page-next') as HTMLButtonElement
const click = (el: HTMLElement) => act(() => { fireEvent.click(el) })
const pageOf = (page: string, entries: unknown[]) => ({ kind: 'ok', value: parseJournalPage(wirePage({ page, entries })) })
const wireOf = (e: JournalEntry) => wireEntry(e.id, { at: e.at, kind: e.kind, text: e.text })

/** The unit A as the store holds it after a session-page read: the current page's rows, the chain, the other pages (when given). */
function seedA(over: Parameters<typeof seedUnit>[3] = {}) {
  setSessionsSupport(HOST, true)
  const key = seedUnit(HOST, 'SA', { kind: 'solo', id: 'a', label: 'Solo A' }, {
    entries: CUR, confirms: [BAND], openConfirms: 2, currentPage: 's-3', sessions: SESSIONS,
    pages: { 's-2': { entries: P2, truncated: false, readAt: 5 }, 's-1': { entries: P1, truncated: false, readAt: 5 } }, ...over,
  })
  patchViewMemo(HOST, key, { journalOpen: true })
  setActive('a')
  return key
}

beforeEach(() => {
  cleanup()
  localStorage.clear()
  resetTeamStores()
  useJournalStore.getState().reset()
  useJournalUiStore.setState({ lastWidths: {} })
  clearViewMemos()
  clearModuleRegistry()
  getJournal.mockReset(); getJournalPage.mockReset(); getJournalSessions.mockReset()
  useShownHostsStore.setState({ ids: [HOST] })
  useI18nStore.getState().setLocale('en')
  Object.defineProperty(window, 'innerWidth', { configurable: true, value: 2200 })
  Object.defineProperty(HTMLElement.prototype, 'scrollHeight', { configurable: true, get() { return heightOf(this) } })
  Object.defineProperty(HTMLElement.prototype, 'clientHeight', { configurable: true, get() { return VIEW } })
  Object.defineProperty(HTMLElement.prototype, 'offsetTop', {
    configurable: true,
    get() {
      const all = Array.from(this.closest('[data-testid="journal-stream"]')?.querySelectorAll('[data-testid="journal-entry"], [data-testid="journal-day"]') ?? [])
      return Math.max(0, all.indexOf(this)) * ROW
    },
  })
  Object.defineProperty(HTMLElement.prototype, 'scrollTop', {
    configurable: true,
    get() { return scrollTops.get(this) ?? 0 },
    set(v: number) { scrollTops.set(this, Math.max(0, Math.min(maxTop(this), v))) },
  })
  seedScene({ members: [['A', 'a-tm']], tabs: [['x', null]], workspaces: [{ id: 'w1', tabs: ['x'] }], activeTabId: 'x' })
  act(() => {
    useTabStore.setState((s) => ({ tabs: { ...s.tabs, a: ccTabOf('a', HOST, 'a-solo', 'SA') }, tabOrder: [...s.tabOrder, 'a'] }))
  })
  setJournalHost(HOST, true)
})
afterEach(() => {
  cleanup()
  Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1024 })
  for (const p of ['scrollHeight', 'clientHeight', 'offsetTop', 'scrollTop']) delete (HTMLElement.prototype as unknown as Record<string, unknown>)[p]
})

describe('opening', () => {
  it('opens on the current session, scrolled to its bottom', () => {
    seedA({ entries: Array.from({ length: 12 }, (_, i) => entry(`c${i + 1}`, D2 + 3_700_000 + i * 1000)) })
    mount()
    expect(ids()).toHaveLength(12)
    expect(stream().scrollTop).toBe(maxTop(stream()))
    expect(stream().scrollTop).toBeGreaterThan(0)
  })
  it('the header names the span and 目前 on the current page', () => {
    seedA()
    mount()
    const head = screen.getByTestId('journal-page-header').textContent!
    expect(head).toContain('2026-10-15 12:45') // S3 started at D2 + 1h
    expect(head).toContain('Current')
  })
})

describe('上一頁 / 下一頁', () => {
  it('on the current page 下一頁 is disabled and 上一頁 is not; a click shows the session before, without 目前', () => {
    seedA()
    mount()
    expect(next().disabled).toBe(true)
    expect(prev().disabled).toBe(false)
    click(prev())
    expect(ids()).toEqual(['p2a', 'p2b'])
    const head = screen.getByTestId('journal-page-header').textContent!
    expect(head).toContain('2026-10-15 11:45')
    expect(head).not.toContain('Current')
    expect(next().disabled).toBe(false)
    expect(readViewMemo(HOST, 'solo:a').page).toBe('s-2')
  })
  it('on the first page 上一頁 is disabled; going back to the last one is the current page again (the choice is "follow the current")', () => {
    seedA()
    mount()
    click(prev()); click(prev())
    expect(ids()).toEqual(['p1a'])
    expect(prev().disabled).toBe(true)
    click(next()); click(next())
    expect(ids()).toEqual(['c1', 'c2'])
    expect(next().disabled).toBe(true)
    expect(readViewMemo(HOST, 'solo:a').page).toBeNull()
  })
  it('a page not held yet is read with only its page id (no limit, no before) and shown when it lands', async () => {
    seedA({ pages: {} })
    getJournalPage.mockResolvedValue(pageOf('s-2', P2.map(wireOf)))
    mount()
    click(prev())
    expect(getJournalPage).toHaveBeenCalledWith(HOST, 'SA', 's-2')
    expect(getJournalPage.mock.calls[0]).toHaveLength(3)
    await waitFor(() => expect(ids()).toEqual(['p2a', 'p2b']))
  })
  it('the 待確認 band and its count do not change with the page', () => {
    seedA()
    mount()
    const bandIds = () => within(screen.getByTestId('journal-confirm-band')).getAllByTestId('journal-entry').map((n) => n.getAttribute('data-entry-id'))
    const count = () => screen.getByTestId('journal-filter-confirm-count').textContent
    expect(bandIds()).toEqual(['band1'])
    expect(count()).toBe('2')
    click(prev()); click(prev())
    expect(bandIds()).toEqual(['band1'])
    expect(count()).toBe('2')
  })
})

describe('filters and live entries stay on their page', () => {
  it('a filter narrows the page being read only, and holds on the other page', () => {
    seedA()
    mount()
    click(screen.getByTestId('journal-filter-task'))
    expect(ids()).toEqual(['c2'])
    click(prev())
    expect(ids()).toEqual(['p2b']) // the task row of THIS page, not the current page's
    click(prev())
    expect(ids()).toEqual([]) // page 1 has no task
  })
  it('a new entry is appended on the current page only: the page being read does not change, and it is there when the person comes back', () => {
    const key = seedA()
    mount()
    click(prev())
    act(() => {
      useJournalStore.setState((s) => {
        const h = s.byHost[HOST]
        const u = h.units[key]
        return { byHost: { ...s.byHost, [HOST]: { ...h, units: { ...h.units, [key]: { ...u, entries: [...u.entries, entry('c3', D2 + 3_900_000)] } } } } }
      })
    })
    expect(ids()).toEqual(['p2a', 'p2b'])
    click(next())
    expect(ids()).toEqual(['c1', 'c2', 'c3'])
  })
})

describe('notices', () => {
  it('truncated shows the cap notice, a page without the flag shows none', () => {
    seedA({ truncated: true })
    mount()
    expect(screen.getByTestId('journal-page-truncated').textContent).toContain('1000')
    click(prev())
    expect(screen.queryByTestId('journal-page-truncated')).toBeNull()
  })
  it('an incomplete chain says earlier sessions may be missing, on the first page only', () => {
    seedA({ sessionsIncomplete: true })
    mount()
    expect(screen.queryByTestId('journal-page-incomplete')).toBeNull()
    click(prev()); click(prev())
    expect(screen.getByTestId('journal-page-incomplete')).toBeTruthy()
  })
})

describe('errors are shown, not read as an empty page', () => {
  it('a 503 on another session\'s page shows an error with a retry, and the retry reads again', async () => {
    seedA({ pages: {} })
    getJournalPage.mockRejectedValueOnce(Object.assign(new Error('unavailable'), { status: 503 }))
    mount()
    click(prev())
    await screen.findByTestId('journal-page-error')
    expect(screen.queryByTestId('journal-empty')).toBeNull()
    getJournalPage.mockResolvedValueOnce(pageOf('s-2', P2.map(wireOf)))
    click(screen.getByTestId('journal-page-retry'))
    await waitFor(() => expect(ids()).toEqual(['p2a', 'p2b']))
    expect(screen.queryByTestId('journal-page-error')).toBeNull()
  })
  it('a failed read of the current session is shown on the card that already holds rows', () => {
    seedA()
    act(() => { useJournalStore.setState({ readFailed: { [sessionKey(HOST, 'SA')]: true } }) })
    mount()
    expect(screen.getByTestId('journal-page-error')).toBeTruthy()
    expect(ids()).toEqual(['c1', 'c2'])
  })
  it('a unit that never loaded because of a 503 says so instead of showing nothing', () => {
    setSessionsSupport(HOST, true)
    useJournalStore.setState({ readFailed: { [sessionKey(HOST, 'SA')]: true }, routes: { [sessionKey(HOST, 'SA')]: { kind: 'direct' } } })
    patchViewMemo(HOST, 'failed:SA', { journalOpen: true }) // the stand-in key the card is opened under while there is no unit
    setActive('a')
    mount()
    expect(selectUnit(useJournalStore.getState(), HOST, 'SA')).toBeUndefined()
    expect(screen.getByTestId('journal-card-error')).toBeTruthy()
  })
})

describe('the first read failed (no unit yet): retry and the stand-in memo', () => {
  const failFirst = () => {
    setSessionsSupport(HOST, true)
    useJournalStore.setState({ readFailed: { [sessionKey(HOST, 'SA')]: true }, routes: { [sessionKey(HOST, 'SA')]: { kind: 'direct' } } })
    patchViewMemo(HOST, 'failed:SA', { journalOpen: true, filter: 'task', widths: { journal: 480, roadmap: null, tasks: null }, fixed: { journal: true, roadmap: false, tasks: false } })
    setActive('a')
  }
  it('the error card has a retry that asks once however many times it is clicked, and on success the card stays open with its state and the stand-in is gone', async () => {
    failFirst()
    mount()
    let release!: (v: unknown) => void
    getJournalPage.mockReturnValue(new Promise((r) => { release = r }))
    getJournalSessions.mockResolvedValue({ kind: 'ok', value: parseSessions({ sessions: [{ session_id: 's-3', started_at: D2, ended_at: 0, via: 'root', current: true }] }) })
    click(screen.getByTestId('journal-page-retry'))
    click(screen.getByTestId('journal-page-retry'))
    expect(getJournalPage).toHaveBeenCalledTimes(1)
    expect(getJournalPage.mock.calls[0]).toHaveLength(2)
    await act(async () => {
      release({ kind: 'ok', value: parseJournalPage(wirePage({ unit: { kind: 'solo', id: 'a', label: 'Solo A' }, page: 's-3', entries: [wireEntry('n1', { at: D2, kind: 'task_new' })] })) })
    })
    await waitFor(() => expect(screen.getByTestId('journal-stream')).toBeTruthy())
    expect(screen.queryByTestId('journal-card-error')).toBeNull()
    expect(screen.getByTestId('journal-card').style.width).toBe('480px')
    expect(screen.getByTestId('journal-filter-task').getAttribute('aria-pressed')).toBe('true')
    expect(readViewMemo(HOST, 'solo:a')).toMatchObject({ journalOpen: true, filter: 'task', fixed: { journal: true } })
    expect(hasViewMemo(HOST, 'failed:SA')).toBe(false)
  })
  it('a retry that fails again leaves the error card and can be tried once more', async () => {
    failFirst()
    mount()
    getJournalPage.mockRejectedValue(new Error('503'))
    click(screen.getByTestId('journal-page-retry'))
    await waitFor(() => expect(getJournalPage).toHaveBeenCalledTimes(1))
    await act(async () => { await Promise.resolve() })
    click(screen.getByTestId('journal-page-retry'))
    await waitFor(() => expect(getJournalPage).toHaveBeenCalledTimes(2))
  })
})

describe('the first-read retry is one flight per target', () => {
  it('a retry still in flight for tab A does not swallow the retry of tab B, and A finishing late changes nothing for B', async () => {
    setSessionsSupport(HOST, true)
    act(() => { useTabStore.setState((s) => ({ tabs: { ...s.tabs, b: ccTabOf('b', HOST, 'b-solo', 'SB') }, tabOrder: [...s.tabOrder, 'b'] })) })
    for (const [sess, id] of [['SA', 'a'], ['SB', 'b']] as const) {
      useJournalStore.setState((s) => ({ readFailed: { ...s.readFailed, [sessionKey(HOST, sess)]: true }, routes: { ...s.routes, [sessionKey(HOST, sess)]: { kind: 'direct' } } }))
      patchViewMemo(HOST, `failed:${sess}`, { journalOpen: true })
      void id
    }
    const pending: Record<string, (v: unknown) => void> = {}
    getJournalPage.mockImplementation((_h: string, sess: string) => new Promise((r) => { pending[sess] = r }))
    setActive('a')
    mount()
    click(screen.getByTestId('journal-page-retry'))
    expect(getJournalPage).toHaveBeenCalledTimes(1)
    setActive('b')
    click(screen.getByTestId('journal-page-retry'))
    expect(getJournalPage.mock.calls.map((c) => c[1])).toEqual(['SA', 'SB'])
    await act(async () => { pending.SA({ kind: 'none' }) }) // A finishes late
    click(screen.getByTestId('journal-page-retry')) // B is still in flight: this click is the second for B
    expect(getJournalPage).toHaveBeenCalledTimes(2)
  })
})

describe('the chain of sessions failed (the current page read fine)', () => {
  it('the rows show, the pager is unusable, the error says so, and its retry asks once', async () => {
    seedA({ sessions: null, sessionsFailed: true })
    mount()
    expect(ids()).toEqual(['c1', 'c2'])
    expect(screen.getByTestId('journal-sessions-error')).toBeTruthy()
    expect(prev().disabled).toBe(true)
    expect(next().disabled).toBe(true)
    getJournalSessions.mockResolvedValue({ kind: 'ok', value: parseSessions({ sessions: SESSIONS.map((x) => ({ session_id: x.sessionId, started_at: x.startedAt, ended_at: x.endedAt, via: x.via, current: x.current })) }) })
    click(screen.getByTestId('journal-page-retry'))
    click(screen.getByTestId('journal-page-retry'))
    expect(getJournalSessions).toHaveBeenCalledTimes(1)
    await waitFor(() => expect(screen.queryByTestId('journal-sessions-error')).toBeNull())
    expect(prev().disabled).toBe(false)
  })
  it('a chain that was held and then failed to re-read: the error shows and 上一頁 / 下一頁 are both disabled (no walking an out-of-date chain); a good retry brings them back', async () => {
    seedA({ sessionsFailed: true })
    mount()
    expect(screen.getByTestId('journal-sessions-error')).toBeTruthy()
    expect(ids()).toEqual(['c1', 'c2']) // the current page itself still shows
    expect(prev().disabled).toBe(true)
    expect(next().disabled).toBe(true)
    getJournalSessions.mockResolvedValue({ kind: 'ok', value: parseSessions({ sessions: SESSIONS.map((x) => ({ session_id: x.sessionId, started_at: x.startedAt, ended_at: x.endedAt, via: x.via, current: x.current })) }) })
    click(screen.getByTestId('journal-page-retry'))
    await waitFor(() => expect(screen.queryByTestId('journal-sessions-error')).toBeNull())
    expect(prev().disabled).toBe(false)
  })
  it('no error when the chain is fine', () => {
    seedA()
    mount()
    expect(screen.queryByTestId('journal-sessions-error')).toBeNull()
  })
})

describe('without journal.sessions.v1', () => {
  it('there is no pager or header, and 載入更早 reads the older month as today', async () => {
    const key = seedA({ more: true }) // the unit still holds session pages from an earlier connection; the host no longer declares them
    setSessionsSupport(HOST, false)
    getJournal.mockResolvedValue({ kind: 'ok', value: parseJournalPage(wirePage({ entries: [wireEntry('old1', { at: D1 })], more: false })) })
    mount()
    expect(screen.queryByTestId('journal-page-prev')).toBeNull()
    expect(screen.queryByTestId('journal-page-header')).toBeNull()
    click(screen.getByTestId('journal-load-older'))
    await waitFor(() => expect(getJournal).toHaveBeenCalledTimes(1))
    expect(getJournal.mock.calls[0][2]).toMatch(/:/) // the before cursor
    expect(getJournalPage).not.toHaveBeenCalled()
    expect(readViewMemo(HOST, key).page).toBeNull()
  })
})
