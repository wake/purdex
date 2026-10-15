// spa/src/components/journal/workbook-off.test.tsx — the workbook view is off on journal.v1 hosts and unchanged on older ones
// (J-2b T-2b.4, spec §12): every place that opened it — the title-bar strip, the panel's drill, the row's button and status line,
// the ended group — and that a persisted drill is ignored, not erased.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, render, screen, within } from '@testing-library/react'
import { TitleBar } from '../TitleBar'
import { PanelAreas } from './CardStack'
import { panelView } from '../team/panel-view'
import { TeamDisplayProvider } from '../team/TeamDisplayProvider'
import type { TeamPanelTeam } from '../team/team-display'
import { HOST, KEY, resetTeamStores, seedScene } from '../../lib/team/__tests__/team-fixture'
import { seedWorkbook } from '../../lib/team/__tests__/workbook-fixture'
import { ccTabOf, seedUnit, setJournalHost } from '../../lib/journal/__tests__/journal-fixture'
import { clearViewMemos, patchViewMemo } from '../../lib/journal/view-memory'
import { clearModuleRegistry } from '../../lib/module-registry'
import { useTabStore } from '../../stores/useTabStore'
import { useJournalStore } from '../../stores/useJournalStore'
import { useJournalUiStore } from '../../stores/useJournalUiStore'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { useWorkbookStore } from '../../stores/useWorkbookStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useI18nStore } from '../../stores/useI18nStore'

vi.mock('../team/TeamSeatIcon', () => ({
  TeamSeatIcon: () => <span data-testid="seat-icon" />,
  TeamSeatHostBadge: () => <span data-testid="seat-host" />,
}))

const DRILL = { hostId: HOST, sessionId: 'A' }
const q = (id: string) => screen.queryByTestId(id)
const setActive = (id: string | null) => act(() => { useTabStore.setState({ activeTabId: id }) })
const addTab = (tab: ReturnType<typeof ccTabOf>) => act(() => { useTabStore.setState((s) => ({ tabs: { ...s.tabs, [tab.id]: tab }, tabOrder: [...s.tabOrder.filter((i) => i !== tab.id), tab.id] })) })
const mount = () => render(<TeamDisplayProvider><TitleBar title="T" /><PanelAreas /></TeamDisplayProvider>)

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
  useI18nStore.getState().setLocale('en')
  seedScene({ members: [['A', 'a-tm']], tabs: [['lead', 'lead-tm'], ['ma', 'a-tm']], workspaces: [{ id: 'w1', tabs: ['lead', 'ma'] }], activeTabId: 'lead' })
  addTab(ccTabOf('lead', HOST, 'lead-tm', 'L'))
  seedWorkbook(HOST, 'A', { status: 'Wiring the login form. Next the tests.' }, { v2: true })
  seedWorkbook(HOST, 'L', { status: 'Planning.' }, { v2: true })
  useTeamUiStore.setState({ panelMode: { [KEY]: 'full' } })
})
afterEach(() => cleanup())

/** Open the team card (journal host) — the older host's area is already in the pane (`full`). */
const openTeamCard = () => {
  useJournalUiStore.getState().setTeamOpen(KEY, true)
  patchViewMemo(HOST, seedUnit(HOST, 'L', { kind: 'team', id: 'tm1', label: 'T' }), { journalOpen: false })
}

describe('panelView', () => {
  const team = { teamKey: KEY, name: 'T' } as TeamPanelTeam
  const drill = { [KEY]: DRILL }
  const own = { hostId: HOST, sessionId: 'S' }
  it('on a journal host a persisted drill is ignored and there is no own workbook', () => {
    expect(panelView({ panelTeam: team, teamDrill: drill, own, journalHost: true })).toEqual({ kind: 'team', team })
    expect(panelView({ panelTeam: null, teamDrill: drill, own, journalHost: true })).toBeNull()
  })
  it('on an older host the same input still drills', () => {
    expect(panelView({ panelTeam: team, teamDrill: drill, own })).toEqual({ kind: 'workbook', from: 'team', team })
    expect(panelView({ panelTeam: null, teamDrill: drill, own })).toEqual({ kind: 'workbook', from: 'own', ...own })
  })
})

describe('the team panel', () => {
  it('journal host: a persisted drill does not open the workbook, and is not erased', () => {
    setJournalHost(HOST, true)
    openTeamCard()
    useTeamUiStore.getState().setTeamDrill(KEY, DRILL)
    mount()
    expect(q('team-seat-workbook')).toBeNull()
    expect(screen.getAllByTestId('team-panel-row').length).toBe(2)
    expect(useTeamUiStore.getState().teamDrill[KEY]).toEqual(DRILL)
  })
  it('older host: the same drill opens the workbook; a journal host in between did not erase it', () => {
    setJournalHost(HOST, true)
    openTeamCard()
    useTeamUiStore.getState().setTeamDrill(KEY, DRILL)
    const r = mount()
    expect(q('team-seat-workbook')).toBeNull()
    r.unmount()
    setJournalHost(HOST, false)
    mount()
    expect(q('team-seat-workbook')).toBeTruthy()
  })
  it('journal host: the rows have no workbook button and no 目前狀況 line; an older host has both', () => {
    setJournalHost(HOST, true)
    openTeamCard()
    const r = mount()
    expect(screen.getAllByTestId('team-panel-row').length).toBe(2)
    expect(q('team-panel-workbook')).toBeNull()
    expect(q('team-panel-task')).toBeNull()
    r.unmount()
    setJournalHost(HOST, false)
    mount()
    expect(screen.getAllByTestId('team-panel-workbook').length).toBe(2)
    expect(screen.getAllByTestId('team-panel-task').length).toBe(2)
  })
  it('the ended group does not offer a drill on a journal host, and does on an older one', () => {
    useTeamUiStore.getState().recordEndedSeats(KEY, [{ hostId: HOST, sessionId: 'Z', title: 'gone Z', endedAt: 1 }])
    setJournalHost(HOST, true)
    openTeamCard()
    const r = mount()
    expect(q('team-panel-ended')).toBeNull()
    r.unmount()
    setJournalHost(HOST, false)
    mount()
    expect(q('team-panel-ended')).toBeTruthy()
  })
})

describe('the title bar', () => {
  it('journal host: no team strip even when the team sits in the title bar, no own strip, no notebook button', () => {
    setJournalHost(HOST, true)
    seedUnit(HOST, 'L', { kind: 'team', id: 'tm1', label: 'T' })
    useTeamUiStore.setState({ panelMode: { [KEY]: 'titlebar' }, sharedPanelMode: 'titlebar' })
    mount()
    expect(q('team-title-strip')).toBeNull()
    expect(q('own-title-strip')).toBeNull()
    expect(q('team-notebook-button')).toBeNull()
  })
  it('older host: the team strip shows in the title bar state', () => {
    setJournalHost(HOST, false)
    useTeamUiStore.setState({ panelMode: { [KEY]: 'titlebar' } })
    mount()
    expect(q('team-title-strip')).toBeTruthy()
  })
  it('an own workbook (a tab of no team) shows nothing on a journal host', () => {
    setActive(null)
    addTab(ccTabOf('solo', HOST, 'solo-tm', 'S9'))
    seedWorkbook(HOST, 'S9', { status: 'Solo status.' }, { v2: true })
    setActive('solo')
    setJournalHost(HOST, true)
    mount()
    expect(q('own-title-strip')).toBeNull()
    expect(q('team-panel-area')).toBeNull()
    expect(within(document.body).queryByText('Solo status.')).toBeNull()
  })
})
