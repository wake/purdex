// spa/src/components/journal/JournalButtons.count.test.tsx — the number on the 日誌 button follows the ticks (J-2c, spec Q-B): the unit
// is read through the real store (the loader's own call), Haiku ticks the items on the daemon, the next read carries fewer open
// items, and the number comes down; at 0 it is gone.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, render, screen } from '@testing-library/react'

const getJournal = vi.fn()
vi.mock('../../lib/journal/api', () => ({ getJournal: (...a: unknown[]) => getJournal(...a), getTasks: vi.fn(), getRoadmap: vi.fn(), setPinned: vi.fn() }))
vi.mock('../team/TeamSeatIcon', () => ({
  TeamSeatIcon: () => <span data-testid="seat-icon" />,
  TeamSeatHostBadge: () => <span data-testid="seat-host" />,
}))

import { TitleBar } from '../TitleBar'
import { TeamDisplayProvider } from '../team/TeamDisplayProvider'
import { HOST, resetTeamStores, seedScene } from '../../lib/team/__tests__/team-fixture'
import { ccTabOf, setJournalHost } from '../../lib/journal/__tests__/journal-fixture'
import { parseJournalPage } from '../../lib/journal/parse'
import { T0, wireConfirm, wirePage } from '../../lib/journal/fixtures'
import { clearViewMemos } from '../../lib/journal/view-memory'
import { clearModuleRegistry } from '../../lib/module-registry'
import { useTabStore } from '../../stores/useTabStore'
import { useJournalStore } from '../../stores/useJournalStore'
import { useJournalUiStore } from '../../stores/useJournalUiStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useI18nStore } from '../../stores/useI18nStore'

const page = (items: Array<[string, boolean]>) => {
  const openItems = items.filter(([, done]) => !done).length
  const c = wireConfirm('c1', { at: T0, pinned: openItems > 0, items: items.map(([text, done]) => ({ text, done })) })
  return { kind: 'ok', value: parseJournalPage(wirePage({ unit: { kind: 'solo', id: 'u1', label: 'solo' }, entries: [c], pinned: openItems > 0 ? [c] : [], open_confirms: openItems })) }
}
const count = () => screen.queryByTestId('journal-journal-count')?.textContent ?? null
const read = () => act(async () => { await useJournalStore.getState().loadJournal(HOST, 'S1') })

beforeEach(() => {
  cleanup()
  localStorage.clear()
  resetTeamStores()
  useJournalStore.getState().reset()
  useJournalUiStore.setState({ lastWidths: {}, teamOpen: {} })
  clearViewMemos()
  clearModuleRegistry()
  getJournal.mockReset()
  useShownHostsStore.setState({ ids: [HOST] })
  useI18nStore.getState().setLocale('en')
  seedScene({ tabs: [['x', null]], workspaces: [{ id: 'w1', tabs: ['x'] }], activeTabId: 'x' })
  act(() => { useTabStore.setState((s) => ({ tabs: { ...s.tabs, cc1: ccTabOf('cc1', HOST, 'solo-tm', 'S1') }, tabOrder: [...s.tabOrder, 'cc1'], activeTabId: 'cc1' })) })
  setJournalHost(HOST, true)
})
afterEach(() => cleanup())

describe('the 日誌 button count', () => {
  it('comes down as items get ticked, and disappears at 0 (M: the count never refreshes)', async () => {
    getJournal.mockResolvedValueOnce(page([['a', false], ['b', false], ['c', false]]))
    await read()
    render(<TeamDisplayProvider><TitleBar title="T" /></TeamDisplayProvider>)
    expect(count()).toBe('3')
    getJournal.mockResolvedValueOnce(page([['a', true], ['b', false], ['c', false]]))
    await read()
    expect(count()).toBe('2')
    getJournal.mockResolvedValueOnce(page([['a', true], ['b', true], ['c', false]]))
    await read()
    expect(count()).toBe('1')
    getJournal.mockResolvedValueOnce(page([['a', true], ['b', true], ['c', true]]))
    await read()
    expect(count()).toBeNull()
  })
  it('a count of 0 from the start shows no badge (M: show on 0)', async () => {
    getJournal.mockResolvedValueOnce(page([['a', true]]))
    await read()
    render(<TeamDisplayProvider><TitleBar title="T" /></TeamDisplayProvider>)
    expect(count()).toBeNull()
    expect(screen.getByTestId('journal-journal-button')).toBeTruthy()
  })
})
