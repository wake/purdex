// spa/src/components/team/TeamPanelArea.own.test.tsx — WA-2b-1b (round 3): a tab of no team whose `cc` conversation has a
// workbook shows that workbook in the area's shared four-state value. Real stores, the real provider and the real TabContent.
import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { TabContent } from '../TabContent'
import { TeamDisplayProvider } from './TeamDisplayProvider'
import { TeamPanelArea } from './TeamPanelArea'
import { HOST, KEY, resetTeamStores, seedScene } from '../../lib/team/__tests__/team-fixture'
import { entry, seedWorkbook } from '../../lib/team/__tests__/workbook-fixture'
import { clearModuleRegistry, registerModule } from '../../lib/module-registry'
import { useTabStore } from '../../stores/useTabStore'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { useWorkbookStore } from '../../stores/useWorkbookStore'
import { useI18nStore } from '../../stores/useI18nStore'
import type { Tab } from '../../types/tab'

const STATUS = 'Wiring the login form. Next the tests.'
const ccTab = (id: string, agent: { type: string; sessionId?: string } | null): Tab => ({
  id, pinned: false, locked: false, createdAt: 0,
  layout: { type: 'leaf', pane: { id: `p-${id}`, content: {
    kind: 'tmux-session', hostId: HOST, sessionCode: `code-${id}`, mode: 'terminal', cachedName: id, tmuxInstance: 'i',
    rebuild: agent ? { sessionName: id, tmuxInstance: 'i', agent: { ...agent, updatedAt: 1 }, capturedAt: 1 } : undefined,
  } } },
})
const addTab = (tab: Tab) => act(() => {
  useTabStore.setState((s) => ({ tabs: { ...s.tabs, [tab.id]: tab }, tabOrder: [...s.tabOrder, tab.id] }))
})
const setActive = (id: string | null) => act(() => { useTabStore.setState({ activeTabId: id }) })
const shared = (mode: 'titlebar' | 'line' | 'full' | 'max') => act(() => { useTeamUiStore.getState().setSharedPanelMode(mode) })

function scene() {
  seedScene({ members: [['A', 'a-tm']], tabs: [['lead', 'lead-tm'], ['ma', 'a-tm'], ['x', null]], workspaces: [{ id: 'w1', tabs: ['lead', 'ma', 'x'] }], activeTabId: 'x' })
  addTab(ccTab('cc1', { type: 'cc', sessionId: 'S1' }))
  seedWorkbook(HOST, 'S1', { status: STATUS, entries: [entry(2), entry(1)] })
}
const mount = () => render(<TeamDisplayProvider><TeamPanelArea /></TeamDisplayProvider>)

beforeEach(() => {
  cleanup()
  localStorage.clear()
  resetTeamStores()
  useWorkbookStore.getState().reset()
  useI18nStore.getState().setLocale('zh-TW')
})
afterEach(() => cleanup())

describe('a tab of no team shows its own conversation workbook', () => {
  it('title bar: the area draws nothing in the pane (the strip in the title bar is the area)', () => {
    scene(); setActive('cc1'); mount()
    expect(screen.queryByTestId('team-panel-area')).toBeNull()
  })
  it('line: one line, the first sentence of the status; a click brings the area to full', () => {
    scene(); setActive('cc1'); shared('line'); mount()
    expect(screen.getByTestId('own-workbook-line-text').textContent).toBe('Wiring the login form')
    expect(screen.queryByTestId('team-seat-workbook')).toBeNull()
    fireEvent.click(screen.getByTestId('own-workbook-line'))
    expect(useTeamUiStore.getState().sharedPanelMode).toBe('full')
    expect(screen.getByTestId('team-seat-workbook')).toBeTruthy()
  })
  it('full: the workbook view, no back control, not enlarged', () => {
    scene(); setActive('cc1'); shared('full'); mount()
    expect(screen.getByTestId('team-seat-workbook-status').textContent).toBe(STATUS)
    expect(screen.getAllByTestId('team-seat-workbook-entry')).toHaveLength(2)
    expect(screen.queryByTestId('team-seat-workbook-back')).toBeNull()
    expect(screen.getByTestId('team-panel-area').getAttribute('data-expanded')).toBe('false')
  })
  it('max: the same view, enlarged', () => {
    scene(); setActive('cc1'); shared('max'); mount()
    expect(screen.getByTestId('team-seat-workbook')).toBeTruthy()
    expect(screen.getByTestId('team-panel-area').getAttribute('data-expanded')).toBe('true')
  })
  it('the header controls: move-to-title-bar (ArrowLineUp) in line and full; expand toggles full / max', () => {
    scene(); setActive('cc1'); shared('line'); mount()
    fireEvent.click(screen.getByTestId('team-panel-to-titlebar'))
    expect(useTeamUiStore.getState().sharedPanelMode).toBe('titlebar')
    shared('full')
    fireEvent.click(screen.getByTestId('team-panel-expand'))
    expect(useTeamUiStore.getState().sharedPanelMode).toBe('max')
    fireEvent.click(screen.getByTestId('team-panel-expand'))
    expect(useTeamUiStore.getState().sharedPanelMode).toBe('full')
    fireEvent.click(screen.getByTestId('team-panel-to-titlebar'))
    expect(useTeamUiStore.getState().sharedPanelMode).toBe('titlebar')
  })
  it('a status with no text yet: the line says so', () => {
    scene(); seedWorkbook(HOST, 'S1', { status: '' }); setActive('cc1'); shared('line'); mount()
    expect(screen.getByTestId('own-workbook-line-text').textContent).toBe('尚無狀態')
  })
  it('a full view holds the conversation in the store while it is open (one viewing)', () => {
    scene(); setActive('cc1'); shared('full'); const { unmount } = mount()
    expect(Object.keys(useWorkbookStore.getState().viewing[HOST] ?? {})).toEqual(['c-S1'])
    unmount()
    expect(Object.keys(useWorkbookStore.getState().viewing[HOST] ?? {})).toEqual([])
  })
})

describe('when there is nothing to show', () => {
  it('no workbook for the conversation', () => {
    scene(); useWorkbookStore.getState().reset(); useWorkbookStore.setState((s) => ({ support: { ...s.support, [HOST]: { v1: true, v2: false } } }))
    setActive('cc1'); shared('full'); mount()
    expect(screen.queryByTestId('team-panel-area')).toBeNull()
  })
  it('a host without workbook.v1', () => {
    scene(); seedWorkbook(HOST, 'S1', { status: 'x' }, { v1: false })
    setActive('cc1'); shared('full'); mount()
    expect(screen.queryByTestId('team-panel-area')).toBeNull()
  })
  it('another agent, or a pane with no recorded session id', () => {
    scene(); addTab(ccTab('cx', { type: 'codex', sessionId: 'S1' })); addTab(ccTab('cn', { type: 'cc' }))
    shared('full'); mount()
    setActive('cx'); expect(screen.queryByTestId('team-panel-area')).toBeNull()
    setActive('cn'); expect(screen.queryByTestId('team-panel-area')).toBeNull()
  })
})

describe('a team tab is still the team view', () => {
  it('shows the team, and its drill, whatever the shared value is', () => {
    scene(); shared('line')
    setActive('lead'); mount()
    expect(screen.getByTestId('team-panel')).toBeTruthy()
    expect(screen.queryByTestId('own-workbook-line')).toBeNull()
    act(() => { useTeamUiStore.getState().setTeamDrill(KEY, { hostId: HOST, sessionId: 'A' }) })
    expect(screen.getByTestId('team-seat-workbook')).toBeTruthy()
    expect(screen.getByTestId('team-seat-workbook-back')).toBeTruthy() // the team's drill keeps its back control
  })
  it('the shared value and the team value do not touch each other', () => {
    scene(); shared('max'); setActive('lead'); mount()
    expect(screen.getByTestId('team-panel').getAttribute('data-mode')).toBe('full')
  })
})

describe('tab switches (real TabContent)', () => {
  it('away and back keeps the shared state and the view', () => {
    clearModuleRegistry()
    registerModule({ id: 'term', name: 'Term', panes: [{ kind: 'tmux-session', component: () => <div data-testid="pane" /> }] })
    function Shell() {
      const tabs = useTabStore((s) => s.tabs)
      const order = useTabStore((s) => s.tabOrder)
      const activeId = useTabStore((s) => s.activeTabId)
      return (
        <TeamDisplayProvider>
          <div className="relative">
            <TabContent activeTab={activeId ? tabs[activeId] ?? null : null} allTabs={order.map((id) => tabs[id]).filter(Boolean)} />
            <TeamPanelArea />
          </div>
        </TeamDisplayProvider>
      )
    }
    scene(); setActive('cc1'); shared('max')
    render(<Shell />)
    expect(screen.getByTestId('team-panel-area').getAttribute('data-expanded')).toBe('true')
    setActive('x')
    expect(screen.queryByTestId('team-panel-area')).toBeNull()
    setActive('lead')
    expect(screen.getByTestId('team-panel').getAttribute('data-mode')).toBe('full')
    setActive('cc1')
    expect(screen.getByTestId('team-panel-area').getAttribute('data-expanded')).toBe('true')
    expect(screen.getByTestId('team-seat-workbook-status').textContent).toBe(STATUS)
    shared('line')
    setActive('x'); setActive('cc1')
    expect(screen.getByTestId('own-workbook-line-text').textContent).toBe('Wiring the login form')
  })
})
