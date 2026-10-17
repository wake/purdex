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
import { clearViewMemos, patchViewMemo, readViewMemo } from '../../lib/journal/view-memory'
import { parseJournalPage } from '../../lib/journal/parse'
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
  useJournalUiStore.setState({ lastWidths: {}, teamOpen: {} })
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

describe('without journal.sessions.v1', () => {
  it('there is no pager or header, and 載入更早 reads the older month as today', async () => {
    const key = seedA({ more: true, currentPage: '', sessions: null, pages: {} })
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
