// spa/src/components/journal/JournalHeader.test.tsx — the journal card's header (J-2c, spec §11): unit name, role line, the live dot of
// the host the journal is read from, the roadmap / tasks toggles, close.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { CardStack } from './CardStack'
import { TeamDisplayProvider } from '../team/TeamDisplayProvider'
import { HOST, resetTeamStores, seedScene } from '../../lib/team/__tests__/team-fixture'
import { ccTabOf, seedUnit, setJournalHost } from '../../lib/journal/__tests__/journal-fixture'
import { clearViewMemos, patchViewMemo, readViewMemo } from '../../lib/journal/view-memory'
import { clearModuleRegistry } from '../../lib/module-registry'
import { useHostStore } from '../../stores/useHostStore'
import { useTabStore } from '../../stores/useTabStore'
import { useJournalStore } from '../../stores/useJournalStore'
import { useJournalUiStore } from '../../stores/useJournalUiStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useI18nStore } from '../../stores/useI18nStore'

vi.mock('../team/TeamSeatIcon', () => ({
  TeamSeatIcon: () => <span data-testid="seat-icon" />,
  TeamSeatHostBadge: () => <span data-testid="seat-host" />,
}))

const SOLO = { kind: 'solo' as const, id: 'a', label: 'Solo A' }
const TEAM_UNIT = { kind: 'team' as const, id: 'tm1', label: 'Journal team' }
const setActive = (id: string) => act(() => { useTabStore.setState({ activeTabId: id }) })
const mount = () => render(<TeamDisplayProvider><CardStack /></TeamDisplayProvider>)
const status = (s: 'connected' | 'disconnected') => act(() => { useHostStore.setState((st) => ({ runtime: { ...st.runtime, [HOST]: { status: s } } })) })

beforeEach(() => {
  cleanup()
  localStorage.clear()
  resetTeamStores()
  useJournalStore.getState().reset()
  useJournalUiStore.setState({ lastWidths: {} })
  clearViewMemos()
  clearModuleRegistry()
  useShownHostsStore.setState({ ids: [HOST] })
  useI18nStore.getState().setLocale('en')
  seedScene({ members: [['A', 'a-tm']], tabs: [['x', null]], workspaces: [{ id: 'w1', tabs: ['x'] }], activeTabId: 'x' })
  act(() => {
    useTabStore.setState((s) => ({
      tabs: { ...s.tabs, a: ccTabOf('a', HOST, 'a-solo', 'SA'), lead: ccTabOf('lead', HOST, 'lead-tm', 'SL') },
      tabOrder: [...s.tabOrder, 'a', 'lead'],
    }))
  })
  setJournalHost(HOST, true)
})
afterEach(() => cleanup())

describe('the journal card header', () => {
  it('shows the unit name and the solo role line', () => {
    patchViewMemo(HOST, seedUnit(HOST, 'SA', SOLO), { journalOpen: true })
    setActive('a')
    mount()
    expect(screen.getByTestId('journal-card').textContent).toContain('Solo A')
    expect(screen.getByTestId('journal-role').textContent).toBe('Journal of this session')
  })
  it('a team shows the leader and the member count, in the chosen language', () => {
    patchViewMemo(HOST, seedUnit(HOST, 'SL', TEAM_UNIT), { journalOpen: true })
    setActive('lead')
    mount()
    expect(screen.getByTestId('journal-role').textContent).toMatch(/^leader .+'s journal · 1 members$/)
    act(() => useI18nStore.getState().setLocale('zh-TW'))
    expect(screen.getByTestId('journal-role').textContent).toMatch(/^leader .+ 的日誌 · 1 位 member$/)
  })
  it('the live dot is green while the host is connected and grey when it is not', () => {
    patchViewMemo(HOST, seedUnit(HOST, 'SA', SOLO), { journalOpen: true })
    setActive('a')
    status('connected')
    mount()
    const dot = screen.getByTestId('journal-live-dot')
    expect(dot.getAttribute('data-live')).toBe('true')
    expect(dot.className).toContain('bg-status-success')
    status('disconnected')
    expect(screen.getByTestId('journal-live-dot').getAttribute('data-live')).toBe('false')
    expect(screen.getByTestId('journal-live-dot').className).not.toContain('bg-status-success')
  })
  it('the toggles open the roadmap and the tasks, the close button closes the journal', () => {
    const key = seedUnit(HOST, 'SA', SOLO)
    patchViewMemo(HOST, key, { journalOpen: true })
    setActive('a')
    mount()
    fireEvent.click(screen.getByTestId('journal-toggle-roadmap'))
    fireEvent.click(screen.getByTestId('journal-toggle-tasks'))
    expect(readViewMemo(HOST, key)).toMatchObject({ roadmapOpen: true, tasksOpen: true })
    fireEvent.click(screen.getByTestId('journal-card-close'))
    expect(readViewMemo(HOST, key).journalOpen).toBe(false)
    expect(screen.queryByTestId('journal-card')).toBeNull()
  })
})
