// spa/src/components/journal/JournalButtons.test.tsx — the title bar's 團隊 / 日誌 buttons (J-2b T-2b.1).
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { TitleBar } from '../TitleBar'
import { TeamDisplayProvider } from '../team/TeamDisplayProvider'
import { HOST, KEY, resetTeamStores, seedScene } from '../../lib/team/__tests__/team-fixture'
import { ccTabOf, seedUnit, setJournalHost } from '../../lib/journal/__tests__/journal-fixture'
import { clearViewMemos, readViewMemo } from '../../lib/journal/view-memory'
import { clearModuleRegistry } from '../../lib/module-registry'
import { useTabStore } from '../../stores/useTabStore'
import { useJournalStore } from '../../stores/useJournalStore'
import { useJournalUiStore } from '../../stores/useJournalUiStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useWorkbookStore } from '../../stores/useWorkbookStore'
import type { Tab } from '../../types/tab'

vi.mock('../team/TeamSeatIcon', () => ({
  TeamSeatIcon: () => <span data-testid="seat-icon" />,
  TeamSeatHostBadge: () => <span data-testid="seat-host" />,
}))

const UNIT = { kind: 'team' as const, id: 'tm1', label: 'Journal team' }
const addTab = (tab: Tab) => act(() => { useTabStore.setState((s) => ({ tabs: { ...s.tabs, [tab.id]: tab }, tabOrder: s.tabOrder.includes(tab.id) ? s.tabOrder : [...s.tabOrder, tab.id] })) })
const setActive = (id: string | null) => act(() => { useTabStore.setState({ activeTabId: id }) })
const mount = () => render(<TeamDisplayProvider><TitleBar title="T" /></TeamDisplayProvider>)
const q = (id: string) => screen.queryByTestId(id)

beforeEach(() => {
  cleanup()
  localStorage.clear()
  resetTeamStores()
  useJournalStore.getState().reset()
  useWorkbookStore.getState().reset()
  useJournalUiStore.setState({ lastWidths: {}, teamOpen: {} })
  clearViewMemos()
  clearModuleRegistry()
  useShownHostsStore.setState({ ids: [HOST] })
  seedScene({ members: [['A', 'a-tm']], tabs: [['lead', 'lead-tm'], ['ma', 'a-tm'], ['x', null]], workspaces: [{ id: 'w1', tabs: ['lead', 'ma', 'x'] }], activeTabId: 'x' })
  addTab(ccTabOf('cc1', HOST, 'solo-tm', 'S1'))
  addTab(ccTabOf('lead', HOST, 'lead-tm', 'SL'))
  useI18nStore.getState().setLocale('en')
})
afterEach(() => cleanup())

describe('the 團隊 / 日誌 buttons on a journal.v1 host', () => {
  it('a team tab has both buttons and no notebook button', () => {
    setJournalHost(HOST, true)
    seedUnit(HOST, 'SL', UNIT)
    setActive('lead')
    mount()
    expect(q('journal-team-button')).toBeTruthy()
    expect(q('journal-journal-button')).toBeTruthy()
    expect(q('team-notebook-button')).toBeNull()
  })
  it('a non-team cc tab has 日誌 only', () => {
    setJournalHost(HOST, true)
    seedUnit(HOST, 'S1', { kind: 'solo', id: 'c1', label: 'solo' })
    setActive('cc1')
    mount()
    expect(q('journal-team-button')).toBeNull()
    expect(q('journal-journal-button')).toBeTruthy()
    expect(q('team-notebook-button')).toBeNull()
  })
  it('a tab with no cc session has no journal button', () => {
    setJournalHost(HOST, true)
    setActive('x')
    mount()
    expect(q('journal-journal-button')).toBeNull()
    expect(q('journal-team-button')).toBeNull()
  })
  it('the 日誌 button carries the open 待確認 count, and nothing at 0', () => {
    setJournalHost(HOST, true)
    seedUnit(HOST, 'S1', { kind: 'solo', id: 'c1', label: 'solo' }, { openConfirms: 3 })
    setActive('cc1')
    mount()
    expect(q('journal-journal-count')?.textContent).toBe('3')
    act(() => { seedUnit(HOST, 'S1', { kind: 'solo', id: 'c1', label: 'solo' }, { openConfirms: 0 }) })
    expect(q('journal-journal-count')).toBeNull()
  })
  it('日誌 toggles the unit memo; 團隊 toggles the team key', () => {
    setJournalHost(HOST, true)
    const key = seedUnit(HOST, 'SL', UNIT)
    setActive('lead')
    mount()
    fireEvent.click(screen.getByTestId('journal-journal-button'))
    expect(readViewMemo(HOST, key).journalOpen).toBe(true)
    expect(screen.getByTestId('journal-journal-button').getAttribute('aria-pressed')).toBe('true')
    fireEvent.click(screen.getByTestId('journal-journal-button'))
    expect(readViewMemo(HOST, key).journalOpen).toBe(false)
    fireEvent.click(screen.getByTestId('journal-team-button'))
    expect(useJournalUiStore.getState().teamOpen[KEY]).toBe(true)
    fireEvent.click(screen.getByTestId('journal-team-button'))
    expect(useJournalUiStore.getState().teamOpen[KEY]).toBeUndefined()
  })
  it('日誌 waits (disabled) until the unit is loaded', () => {
    setJournalHost(HOST, true)
    setActive('cc1')
    mount()
    expect((screen.getByTestId('journal-journal-button') as HTMLButtonElement).disabled).toBe(true)
  })
  it('a disconnect (support unknown) keeps the journal buttons: the sticky answer stays', () => {
    setJournalHost(HOST, true)
    seedUnit(HOST, 'S1', { kind: 'solo', id: 'c1', label: 'solo' })
    setActive('cc1')
    mount()
    act(() => useJournalStore.getState().fence(HOST))
    expect(q('journal-journal-button')).toBeTruthy()
  })
})

describe('an older host keeps the notebook button', () => {
  it.each([[false], [null]])('sticky answer %s: notebook button, no journal buttons', (answer) => {
    setJournalHost(HOST, answer)
    setActive('lead')
    mount()
    expect(q('team-notebook-button')).toBeTruthy()
    expect(q('journal-team-button')).toBeNull()
    expect(q('journal-journal-button')).toBeNull()
  })
})
