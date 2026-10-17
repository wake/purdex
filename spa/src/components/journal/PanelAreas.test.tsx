// spa/src/components/journal/PanelAreas.test.tsx — JV-2 (journal-v5 spec §1 U1-U2, §3.1, §5 Q1): the team panel is the docked area on
// EVERY host (full / one line / the title bar, no max), the journal cards float beside it, 「團隊」 is a minimise / restore, and each
// team keeps its own state when the tab moves between a journal.v1 and an older host. Real stores, real TitleBar and TabContent.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { TitleBar } from '../TitleBar'
import { TabContent } from '../TabContent'
import { PanelAreas } from './CardStack'
import { TeamDisplayProvider } from '../team/TeamDisplayProvider'
import { HOST, KEY, member, resetTeamStores, seedScene, sess, tabOn } from '../../lib/team/__tests__/team-fixture'
import { ccTabOf, seedUnit, setJournalHost } from '../../lib/journal/__tests__/journal-fixture'
import { clearViewMemos, patchViewMemo, readViewMemo } from '../../lib/journal/view-memory'
import { clearModuleRegistry, registerModule } from '../../lib/module-registry'
import { useTabStore } from '../../stores/useTabStore'
import { useJournalStore } from '../../stores/useJournalStore'
import { useJournalUiStore } from '../../stores/useJournalUiStore'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { useSessionStore } from '../../stores/useSessionStore'
import { useWorkbookStore } from '../../stores/useWorkbookStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useI18nStore } from '../../stores/useI18nStore'

vi.mock('../team/TeamSeatIcon', () => ({
  TeamSeatIcon: () => <span data-testid="seat-icon" />,
  TeamSeatHostBadge: () => <span data-testid="seat-host" />,
}))

const UNIT = { kind: 'team' as const, id: 'tm1', label: 'Journal team' }
const T2 = 't2'
const KEY2 = `${HOST}\u0000${T2}`
const q = (id: string) => screen.queryByTestId(id)
const mode = (key = KEY) => useTeamUiStore.getState().panelMode[key] ?? 'full'
const setActive = (id: string | null) => act(() => { useTabStore.setState({ activeTabId: id }) })
const addTab = (tab: ReturnType<typeof ccTabOf>) => act(() => { useTabStore.setState((s) => ({ tabs: { ...s.tabs, [tab.id]: tab }, tabOrder: [...s.tabOrder.filter((i) => i !== tab.id), tab.id] })) })
const teamButton = () => screen.getByTestId('journal-team-button')

function Shell() {
  const tabs = useTabStore((s) => s.tabs)
  const order = useTabStore((s) => s.tabOrder)
  const activeId = useTabStore((s) => s.activeTabId)
  return (
    <TeamDisplayProvider>
      <TitleBar title="T" />
      <div className="relative">
        <TabContent activeTab={activeId ? tabs[activeId] ?? null : null} allTabs={order.map((id) => tabs[id]).filter(Boolean)} />
        <PanelAreas />
      </div>
    </TeamDisplayProvider>
  )
}

/** A second team (lead L2 + member D) on the same host, each with a tab. */
function addSecondTeam() {
  const roster = structuredClone(useTeamRosterStore.getState().byHost[HOST])
  roster.push({ id: T2, host_id: 'daemon', created_at: 2, team_name: 'Second', team_label: '', lead: sess('L2', 'lead2-tm'), members: [member('D', 1, 'd-tm')] })
  useTeamRosterStore.setState({ byHost: { [HOST]: roster } })
  const sessions = useSessionStore.getState().sessions[HOST]
  useSessionStore.setState({ sessions: { [HOST]: [...sessions, ...['lead2-tm', 'd-tm'].map((name) => ({ code: `code-${name}`, name, mode: 'terminal', cwd: '~' }))] as never } })
  const extra = [tabOn('lead2', 'lead2-tm'), tabOn('md', 'd-tm')]
  const tabs = useTabStore.getState()
  useTabStore.setState({ tabs: { ...tabs.tabs, ...Object.fromEntries(extra.map((t) => [t.id, t])) }, tabOrder: [...tabs.tabOrder, 'lead2', 'md'] })
  useWorkspaceStore.setState({ workspaces: [{ id: 'w1', name: 'w1', tabs: ['lead', 'ma', 'x', 'lead2', 'md'], activeTabId: null, moduleConfig: {} }], activeWorkspaceId: 'w1' })
}

beforeEach(() => {
  cleanup()
  localStorage.clear()
  resetTeamStores()
  useJournalStore.getState().reset()
  useWorkbookStore.getState().reset()
  useJournalUiStore.setState({ lastWidths: {} })
  clearViewMemos()
  clearModuleRegistry()
  registerModule({ id: 'term', name: 'Term', panes: [{ kind: 'tmux-session', component: () => <div data-testid="pane" /> }] })
  useShownHostsStore.setState({ ids: [HOST] })
  useI18nStore.getState().setLocale('en')
  seedScene({ members: [['A', 'a-tm']], tabs: [['lead', 'lead-tm'], ['ma', 'a-tm'], ['x', null]], workspaces: [{ id: 'w1', tabs: ['lead', 'ma', 'x'] }], activeTabId: 'lead' })
  addTab(ccTabOf('lead', HOST, 'lead-tm', 'SL'))
})
afterEach(() => cleanup())

describe.each([['a journal.v1 host', true], ['an older host', false]] as const)('the team panel on %s', (_name, journal) => {
  beforeEach(() => {
    setJournalHost(HOST, journal)
    if (journal) seedUnit(HOST, 'SL', UNIT)
  })

  it('is docked in the pane in full by default, with the lead and every member (the stack holds no team card)', () => {
    render(<Shell />)
    expect(screen.getByTestId('team-panel').getAttribute('data-mode')).toBe('full')
    expect(within(screen.getByTestId('team-panel-area')).getAllByTestId('team-panel-row')).toHaveLength(2)
    expect(q('journal-team-card')).toBeNull()
  })

  it('has three states and no fourth: full -> one line (header click) -> title bar (⌃) with the strip; no enlarge control, no max', () => {
    render(<Shell />)
    expect(q('team-panel-expand')).toBeNull()
    fireEvent.click(screen.getByTestId('team-panel-count')) // header: full -> line
    expect(screen.getByTestId('team-panel').getAttribute('data-mode')).toBe('line')
    expect(q('team-panel-expand')).toBeNull()
    fireEvent.click(screen.getByTestId('team-panel-to-titlebar'))
    expect(mode()).toBe('titlebar')
    expect(q('team-panel-area')).toBeNull()
    const strip = screen.getByTestId('team-title-strip')
    expect(within(strip).getByTestId('team-strip-name').textContent).toBeTruthy()
    expect(within(strip).getAllByTestId('team-panel-cell').map((c) => c.getAttribute('data-session-id'))).toEqual(['L', 'A'])
    expect(document.querySelector('[data-expanded]')).toBeNull()
  })

  it('the strip brings the panel back to the state it left', () => {
    act(() => useTeamUiStore.getState().setPanelMode(KEY, 'line'))
    act(() => useTeamUiStore.getState().setPanelMode(KEY, 'titlebar'))
    render(<Shell />)
    fireEvent.click(screen.getByTestId('team-strip-name'))
    expect(mode()).toBe('line')
    expect(screen.getByTestId('team-panel').getAttribute('data-mode')).toBe('line')
  })
})

describe('the 團隊 and 工作簿 buttons (journal host)', () => {
  beforeEach(() => {
    setJournalHost(HOST, true)
    seedUnit(HOST, 'SL', UNIT)
  })

  it('團隊: full -> title bar -> full; one line -> title bar (a toggle between line and full would fail this)', () => {
    render(<Shell />)
    fireEvent.click(teamButton())
    expect(mode()).toBe('titlebar')
    expect(q('team-panel-area')).toBeNull()
    expect(q('team-title-strip')).toBeTruthy()
    fireEvent.click(teamButton())
    expect(mode()).toBe('full')
    expect(screen.getByTestId('team-panel').getAttribute('data-mode')).toBe('full')
    act(() => useTeamUiStore.getState().setPanelMode(KEY, 'line'))
    fireEvent.click(teamButton())
    expect(mode()).toBe('titlebar')
  })

  it('工作簿 opens and closes only the journal card; the panel and the other cards stay as they were', () => {
    const key = `team:${UNIT.id}`
    render(<Shell />)
    fireEvent.click(screen.getByTestId('journal-journal-button'))
    expect(readViewMemo(HOST, key)).toMatchObject({ journalOpen: true, roadmapOpen: false, tasksOpen: false })
    expect(screen.getByTestId('journal-card')).toBeTruthy()
    expect(q('journal-roadmap-card')).toBeNull()
    expect(q('journal-tasks-card')).toBeNull()
    expect(mode()).toBe('full')
    fireEvent.click(screen.getByTestId('journal-journal-button'))
    expect(q('journal-card')).toBeNull()
    expect(q('team-panel-area')).toBeTruthy()
  })

  it('the journal cards float left of the panel while it is in the pane, and use the whole width when it is in the title bar', () => {
    patchViewMemo(HOST, `team:${UNIT.id}`, { journalOpen: true })
    useTeamUiStore.setState({ panel: { width: 400, followsMin: false } })
    render(<Shell />)
    const stack = () => screen.getByTestId('journal-card-stack')
    expect(stack().style.right).toBe('424px') // 12 margin + 400 panel + 12 gap
    act(() => useTeamUiStore.getState().setPanelMode(KEY, 'line'))
    expect(stack().style.right).toBe('424px')
    act(() => useTeamUiStore.getState().setPanelMode(KEY, 'titlebar'))
    expect(stack().style.right).toBe('12px')
  })

  it('a tab of no team has no panel and no 團隊 button, and the cards use the whole width', () => {
    addTab(ccTabOf('solo', HOST, 'solo-tm', 'S9'))
    seedUnit(HOST, 'S9', { kind: 'solo', id: 'c9', label: 'solo' })
    patchViewMemo(HOST, 'solo:c9', { journalOpen: true })
    setActive('solo')
    render(<Shell />)
    expect(q('team-panel-area')).toBeNull()
    expect(q('journal-team-button')).toBeNull()
    expect(screen.getByTestId('journal-card-stack').style.right).toBe('12px')
  })
})

describe('each team keeps its own state across journal and older hosts (real TabContent)', () => {
  it('a title-bar team and a one-line team keep their states while the host flips between journal.v1 and older', () => {
    seedUnit(HOST, 'SL', UNIT)
    addSecondTeam()
    addTab(ccTabOf('lead2', HOST, 'lead2-tm', 'SL2'))
    seedUnit(HOST, 'SL2', { kind: 'team', id: 'tm2', label: 'Second' })
    setJournalHost(HOST, true)
    act(() => { useTeamUiStore.getState().setPanelMode(KEY, 'titlebar'); useTeamUiStore.getState().setPanelMode(KEY2, 'line') })
    render(<Shell />)
    expect(q('team-title-strip')).toBeTruthy()
    expect(q('team-panel-area')).toBeNull()

    setActive('lead2')
    expect(q('team-title-strip')).toBeNull()
    expect(screen.getByTestId('team-panel').getAttribute('data-mode')).toBe('line')

    act(() => setJournalHost(HOST, false)) // the same teams on an older host
    expect(screen.getByTestId('team-panel').getAttribute('data-mode')).toBe('line')
    setActive('lead')
    expect(q('team-title-strip')).toBeTruthy()
    expect(q('team-panel-area')).toBeNull()

    act(() => setJournalHost(HOST, true))
    expect(q('team-title-strip')).toBeTruthy()
    setActive('lead2')
    expect(screen.getByTestId('team-panel').getAttribute('data-mode')).toBe('line')
    expect(mode(KEY)).toBe('titlebar')
    expect(mode(KEY2)).toBe('line')
  })
})
