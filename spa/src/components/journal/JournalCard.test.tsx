// spa/src/components/journal/JournalCard.test.tsx — the journal card wired to the real stores (J-2c): the filters narrow the stream, the
// person's name comes from the unit's host, refs link only with the project's remote URL, a pin click is optimistic, the band and
// the older-month button reach the store. The pieces are tested alone in their own files; this is the wiring.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'

const getJournal = vi.fn()
const setPinned = vi.fn()
vi.mock('../../lib/journal/api', () => ({
  getJournal: (...a: unknown[]) => getJournal(...a),
  getTasks: vi.fn(),
  getRoadmap: vi.fn(),
  setPinned: (...a: unknown[]) => setPinned(...a),
}))
vi.mock('../team/TeamSeatIcon', () => ({
  TeamSeatIcon: () => <span data-testid="seat-icon" />,
  TeamSeatHostBadge: () => <span data-testid="seat-host" />,
}))

import { CardStack } from './CardStack'
import { TeamDisplayProvider } from '../team/TeamDisplayProvider'
import { HOST, resetTeamStores, seedScene } from '../../lib/team/__tests__/team-fixture'
import { ccTabOf, seedUnit, setJournalHost } from '../../lib/journal/__tests__/journal-fixture'
import { clearViewMemos, patchViewMemo, readViewMemo } from '../../lib/journal/view-memory'
import { clearModuleRegistry } from '../../lib/module-registry'
import { useTabStore } from '../../stores/useTabStore'
import { selectUnit, useJournalStore } from '../../stores/useJournalStore'
import { useJournalUiStore } from '../../stores/useJournalUiStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useI18nStore } from '../../stores/useI18nStore'
import type { JournalEntry } from '../../lib/journal/types'

const AT = new Date(2026, 9, 14, 14, 5).getTime()
const entry = (id: string, over: Partial<JournalEntry> = {}): JournalEntry => ({
  id, kind: 'exec', by: '', at: AT, actor: 'Adam', text: `text ${id}`, refs: [], items: [], detail: '', pinned: false, ...over,
})
const E_EXEC = entry('e1', { at: AT, actor: 'Adam → Wake', text: 'Wake please look', refs: ['#2829', '7bcf08-12'] })
const E_TASK = entry('t1', { kind: 'task_new', at: AT + 1000, text: '#17 a task' })
const E_CONF = entry('c1', { kind: 'confirm', at: AT + 2000, actor: 'Kenji → Wake', pinned: true, items: [{ text: 'q1', done: false }], text: 'decide' })

const setActive = (id: string) => act(() => { useTabStore.setState({ activeTabId: id }) })
const mount = () => render(<TeamDisplayProvider><CardStack /></TeamDisplayProvider>)
const stream = () => within(screen.getByTestId('journal-stream'))
const streamIds = () => stream().queryAllByTestId('journal-entry').map((n) => n.getAttribute('data-entry-id'))
const unitOf = (s: string) => selectUnit(useJournalStore.getState(), HOST, s)!

beforeEach(() => {
  cleanup()
  localStorage.clear()
  resetTeamStores()
  useJournalStore.getState().reset()
  useJournalUiStore.setState({ lastWidths: {}, teamOpen: {} })
  clearViewMemos()
  clearModuleRegistry()
  getJournal.mockReset(); setPinned.mockReset()
  setPinned.mockResolvedValue({ kind: 'ok', value: true })
  useShownHostsStore.setState({ ids: [HOST] })
  useI18nStore.getState().setLocale('en')
  seedScene({ members: [['A', 'a-tm']], tabs: [['x', null]], workspaces: [{ id: 'w1', tabs: ['x'] }], activeTabId: 'x' })
  act(() => {
    useTabStore.setState((s) => ({
      tabs: { ...s.tabs, a: ccTabOf('a', HOST, 'a-solo', 'SA'), b: ccTabOf('b', HOST, 'b-solo', 'SB') },
      tabOrder: [...s.tabOrder, 'a', 'b'],
    }))
  })
  setJournalHost(HOST, true)
})
afterEach(() => cleanup())

const seedA = (over: Parameters<typeof seedUnit>[3] = {}) => {
  const key = seedUnit(HOST, 'SA', { kind: 'solo', id: 'a', label: 'Solo A' }, { entries: [E_EXEC, E_TASK, E_CONF], pinned: [E_CONF], openConfirms: 1, ...over })
  patchViewMemo(HOST, key, { journalOpen: true })
  setActive('a')
  return key
}

describe('the journal card', () => {
  it('draws the stream, the band and the open count on 待確認', () => {
    seedA()
    mount()
    expect(streamIds()).toEqual(['e1', 't1', 'c1'])
    expect(within(screen.getByTestId('journal-pinned-band')).getAllByTestId('journal-entry').map((n) => n.getAttribute('data-entry-id'))).toEqual(['c1'])
    expect(screen.getByTestId('journal-filter-confirm-count').textContent).toBe('1')
  })
  it('a filter narrows the stream and clicking it again shows everything (the band stays)', () => {
    const key = seedA()
    mount()
    fireEvent.click(screen.getByTestId('journal-filter-task'))
    expect(streamIds()).toEqual(['t1'])
    expect(readViewMemo(HOST, key).filter).toBe('task')
    expect(screen.getByTestId('journal-pinned-band')).toBeTruthy()
    fireEvent.click(screen.getByTestId('journal-filter-task'))
    expect(streamIds()).toEqual(['e1', 't1', 'c1'])
    fireEvent.click(screen.getByTestId('journal-filter-confirm'))
    expect(streamIds()).toEqual(['c1'])
    fireEvent.click(screen.getByTestId('journal-filter-comm'))
    expect(streamIds()).toEqual([])
    expect(screen.getByTestId('journal-empty').textContent).toBe('No matching events')
  })
  it('the highlighted name is the unit host\'s own: two units, two names', () => {
    seedA({ personName: 'Wake' })
    const keyB = seedUnit(HOST, 'SB', { kind: 'solo', id: 'b', label: 'Solo B' }, { entries: [E_EXEC], personName: 'Mia' })
    patchViewMemo(HOST, keyB, { journalOpen: true })
    mount()
    expect(stream().getAllByTestId('journal-person').map((n) => n.textContent)).toEqual(['Wake', 'Wake', 'Wake']) // e1 actor + text, c1 actor
    setActive('b')
    expect(stream().queryAllByTestId('journal-person')).toEqual([])
    cleanup()
    seedUnit(HOST, 'SB', { kind: 'solo', id: 'b', label: 'Solo B' }, { entries: [entry('m1', { actor: 'Adam → Mia', text: 'Mia, and Wake' })], personName: 'Mia' })
    mount()
    expect(stream().getAllByTestId('journal-person').map((n) => n.textContent)).toEqual(['Mia', 'Mia'])
  })
  it('a unit without a person name highlights nothing', () => {
    seedA({ personName: '' })
    mount()
    expect(stream().queryAllByTestId('journal-person')).toEqual([])
  })
  it('refs link to the project\'s issues only when the project has a remote URL; a task number never links', () => {
    seedA({ project: { name: 'purdex', remoteUrl: 'https://github.com/wake/purdex' } })
    mount()
    const links = stream().getAllByRole('link')
    expect(links.map((a) => a.getAttribute('href'))).toEqual(['https://github.com/wake/purdex/issues/2829'])
    expect(stream().getByText('7bcf08-12').closest('a')).toBeNull()
    cleanup()
    seedA({ project: { name: 'purdex', remoteUrl: '' } })
    mount()
    expect(stream().queryAllByRole('link')).toEqual([])
  })
  it('a pin click shows at once, asks the unit\'s host, and the band follows', () => {
    seedA()
    mount()
    const row = stream().getAllByTestId('journal-entry').find((n) => n.getAttribute('data-entry-id') === 'e1')!
    fireEvent.click(within(row).getByTestId('journal-pin-toggle'))
    expect(setPinned).toHaveBeenCalledWith(HOST, 'SA', 'e1', true)
    expect(unitOf('SA').pinned.map((e) => e.id)).toEqual(['c1', 'e1'])
    expect(within(screen.getByTestId('journal-pinned-band')).getAllByTestId('journal-entry').map((n) => n.getAttribute('data-entry-id'))).toEqual(['c1', 'e1'])
  })
  it('a 待確認 can be unpinned by hand; its items stay as they were', () => {
    seedA()
    mount()
    const band = within(screen.getByTestId('journal-pinned-band'))
    fireEvent.click(band.getByTestId('journal-pin-toggle'))
    expect(setPinned).toHaveBeenCalledWith(HOST, 'SA', 'c1', false)
    expect(screen.queryByTestId('journal-pinned-band')).toBeNull()
    expect(unitOf('SA').entries.find((e) => e.id === 'c1')!.items).toEqual([{ text: 'q1', done: false }])
    expect(streamIds()).toContain('c1')
  })
  it('the older-month button asks the unit\'s host for the page before the oldest entry', () => {
    seedA({ more: true })
    getJournal.mockResolvedValue({ kind: 'none' })
    mount()
    fireEvent.click(screen.getByTestId('journal-load-older'))
    expect(getJournal).toHaveBeenCalledTimes(1)
    expect(getJournal.mock.calls[0].slice(0, 2)).toEqual([HOST, 'SA'])
    expect(getJournal.mock.calls[0][2]).toMatch(/^2026-10:e1$/)
  })
})
