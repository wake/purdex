// spa/src/components/journal/useJournalTarget.test.tsx — which host decides the journal UI, and the unreachable stand-in's memo (J-2b R2 A-1 / A-2).
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, render, renderHook, screen } from '@testing-library/react'
import type { ReactNode } from 'react'
import { useJournalTarget } from './useJournalTarget'
import { CardStack } from './CardStack'
import { TeamDisplayProvider } from '../team/TeamDisplayProvider'
import { HOST, resetTeamStores, seedScene } from '../../lib/team/__tests__/team-fixture'
import { ccTabOf, seedUnit, setJournalHost } from '../../lib/journal/__tests__/journal-fixture'
import { clearViewMemos } from '../../lib/journal/view-memory'
import { clearModuleRegistry } from '../../lib/module-registry'
import { useTabStore } from '../../stores/useTabStore'
import { useJournalStore } from '../../stores/useJournalStore'
import { useJournalUiStore } from '../../stores/useJournalUiStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useI18nStore } from '../../stores/useI18nStore'

vi.mock('../team/TeamSeatIcon', () => ({
  TeamSeatIcon: () => <span data-testid="seat-icon" />,
  TeamSeatHostBadge: () => <span data-testid="seat-host" />,
}))

const LEAD_HOST = 'host-b'
const UNIT = { kind: 'team' as const, id: 'tm1', label: 'Remote team' }
const wrapper = ({ children }: { children: ReactNode }) => <TeamDisplayProvider>{children}</TeamDisplayProvider>
const follow = () => useJournalStore.getState().setRoute(HOST, 'SA', { kind: 'follow', hostId: LEAD_HOST, sessionId: 'L', leadHostId: 'lead-daemon' })
const unreachable = () => useJournalStore.getState().setRoute(HOST, 'SA', { kind: 'unreachable', leadHostId: 'lead-daemon', leadSessionId: 'L' })

beforeEach(() => {
  cleanup()
  localStorage.clear()
  resetTeamStores()
  useJournalStore.getState().reset()
  useJournalUiStore.setState({ lastWidths: {}, teamOpen: {} })
  clearViewMemos()
  clearModuleRegistry()
  useShownHostsStore.setState({ ids: [HOST, LEAD_HOST] })
  useI18nStore.getState().setLocale('en')
  seedScene({ members: [['A', 'a-tm']], tabs: [['x', null]], workspaces: [{ id: 'w1', tabs: ['x'] }], activeTabId: 'x' })
  act(() => {
    useTabStore.setState((s) => ({ tabs: { ...s.tabs, a: ccTabOf('a', HOST, 'a-tm', 'SA') }, tabOrder: [...s.tabOrder, 'a'], activeTabId: 'a' }))
  })
})
afterEach(() => cleanup())

describe('A-1: the read host decides', () => {
  it('origin journal.v1, lead host without it: the workbook view stays, no journal key', () => {
    setJournalHost(HOST, true)
    setJournalHost(LEAD_HOST, false)
    follow()
    const { result } = renderHook(() => useJournalTarget(), { wrapper })
    expect(result.current.read?.hostId).toBe(LEAD_HOST)
    expect(result.current.journalHost).toBe(false)
    render(<TeamDisplayProvider><CardStack /></TeamDisplayProvider>)
    expect(screen.queryByTestId('journal-card-stack')).toBeNull()
  })
  it('origin without journal.v1, lead host with it: the journal UI, reading the lead host unit', () => {
    setJournalHost(HOST, false)
    setJournalHost(LEAD_HOST, true)
    seedUnit(LEAD_HOST, 'L', UNIT)
    follow()
    const { result } = renderHook(() => useJournalTarget(), { wrapper })
    expect(result.current.journalHost).toBe(true)
    expect(result.current.memoHost).toBe(LEAD_HOST)
    expect(result.current.memoKey).toBe('team:tm1')
  })
  it('route unresolved: the tab own host decides; once it resolves the read host takes over', () => {
    setJournalHost(HOST, true)
    setJournalHost(LEAD_HOST, false)
    unreachable()
    const { result } = renderHook(() => useJournalTarget(), { wrapper })
    expect(result.current.journalHost).toBe(true)
    expect(result.current.memoKey).toBe('unreachable:lead-daemon')
    act(() => { follow() })
    expect(result.current.journalHost).toBe(false)
  })
})
