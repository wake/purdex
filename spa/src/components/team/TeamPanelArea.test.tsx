// spa/src/components/team/TeamPanelArea.test.tsx — the panel area and its team view (plan TI-4 / WA-2a): who it shows for,
// what a row carries, the two modes, resize (draft-then-commit, clamped), enlarge, and that all of it lives in the store
// (survives a tab switch through the real TabContent and a reload). Real stores and the real provider.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { TabContent } from '../TabContent'
import { TeamDisplayProvider } from './TeamDisplayProvider'
import { TeamPanelArea } from './TeamPanelArea'
import { HOST, KEY, resetTeamStores, seedScene } from '../../lib/team/__tests__/team-fixture'
import { clearModuleRegistry, registerModule } from '../../lib/module-registry'
import { useTabStore } from '../../stores/useTabStore'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import type { TeamRoster } from '../../lib/team/roster'

// The light, the subagent dots and the host chip are TI-3's pieces over the agent store; here they are stand-ins that show
// what the panel asked of them.
vi.mock('./TeamSeatIcon', () => ({
  TeamSeatIcon: ({ sessionCode, subagents }: { sessionCode: string; subagents?: boolean }) => (
    <span data-testid="seat-icon" data-code={sessionCode} data-subagents={String(subagents === true)} />
  ),
  TeamSeatHostBadge: ({ hostId }: { hostId: string }) => <span data-testid="seat-host" data-host={hostId} />,
}))

const MEMBERS: Array<[string, string]> = [['A', 'a-tm'], ['B', 'b-tm'], ['C', 'c-tm']]
const scene = (over: { activeTabId?: string | null; teamName?: string; teamLabel?: string } = {}) => seedScene({
  members: MEMBERS,
  tabs: [['lead', 'lead-tm'], ['ma', 'a-tm'], ['mb', 'b-tm'], ['x', null]],
  workspaces: [{ id: 'w1', tabs: ['lead', 'ma', 'mb', 'x'] }],
  activeTabId: 'lead',
  ...over,
})

const readings = (patch: (t: TeamRoster) => void) => act(() => {
  const roster = structuredClone(useTeamRosterStore.getState().byHost[HOST])
  patch(roster[0])
  useTeamRosterStore.setState({ byHost: { [HOST]: roster } })
})

const mount = () => render(<TeamDisplayProvider><TeamPanelArea /></TeamDisplayProvider>)
const setActive = (id: string | null) => act(() => { useTabStore.setState({ activeTabId: id }) })
const rows = () => screen.getAllByTestId('team-panel-row')
const area = () => screen.getByTestId('team-panel-area')
const STORAGE = 'purdex-team-ui'

beforeEach(() => {
  cleanup()
  localStorage.clear()
  resetTeamStores()
  useTeamUiStore.setState({ panel: { width: 312, expanded: false }, teamDrill: {}, workbookTabs: {} })
  useShownHostsStore.setState({ ids: [HOST] })
  clearModuleRegistry()
})
afterEach(() => cleanup())

describe('who the panel shows for', () => {
  it('panel shows for a lead tab and a member tab, not for others', () => {
    scene()
    mount()
    expect(screen.queryByTestId('team-panel')).not.toBeNull()
    setActive('ma')
    expect(screen.queryByTestId('team-panel')).not.toBeNull()
    setActive('x')
    expect(screen.queryByTestId('team-panel')).toBeNull()
    setActive(null)
    expect(screen.queryByTestId('team-panel')).toBeNull()
  })

  it('a workbook result (toggle on, or a drill) renders no team view yet', () => {
    scene()
    mount()
    act(() => useTeamUiStore.getState().setWorkbookTab('lead', true))
    expect(screen.queryByTestId('team-panel')).toBeNull()
    act(() => { useTeamUiStore.getState().setWorkbookTab('lead', false); useTeamUiStore.getState().setTeamDrill(KEY, { hostId: HOST, sessionId: 'A' }) })
    expect(screen.queryByTestId('team-panel')).toBeNull()
    act(() => useTeamUiStore.getState().setTeamDrill(KEY, null))
    expect(screen.queryByTestId('team-panel')).not.toBeNull()
  })

  it('active row = the active tab\'s seat', () => {
    scene({ activeTabId: 'mb' })
    mount()
    const active = rows().filter((r) => r.getAttribute('data-active') === 'true')
    expect(active.map((r) => r.getAttribute('data-session-id'))).toEqual(['B'])
    setActive('lead')
    expect(rows().filter((r) => r.getAttribute('data-active') === 'true').map((r) => r.getAttribute('data-session-id'))).toEqual(['L'])
  })
})

describe('full mode', () => {
  it('pressing a row keeps the keyboard in the terminal (mousedown is default-prevented, Enter still opens)', () => {
    scene()
    mount()
    const term = document.createElement('textarea') // a stand-in for the terminal's input
    document.body.appendChild(term)
    term.focus()
    const row = rows()[0]
    // A browser moves focus to the focusable row unless mousedown is default-prevented; jsdom does not, so do it by hand.
    if (fireEvent.mouseDown(row)) row.focus()
    expect(document.activeElement).toBe(term)
    term.remove()
  })

  it('lead row is first and not draggable; members are', () => {
    scene()
    mount()
    expect(rows().map((r) => r.getAttribute('data-session-id'))).toEqual(['L', 'A', 'B', 'C'])
    expect(rows()[0].getAttribute('data-role')).toBe('lead')
    expect(rows()[0].getAttribute('draggable')).not.toBe('true')
    expect(rows().slice(1).every((r) => r.getAttribute('draggable') === 'true')).toBe(true)
  })

  it('full rows carry host chip / title / model / effort / context / light / subagents', () => {
    scene()
    readings((t) => {
      t.lead.model = 'claude-opus-5-5'
      t.lead.effort = 'high'
      t.lead.context = { used_percentage: 42.4, window: 1000, at: 1 }
      t.members[0].context = { used_percentage: 7, window: 1000, model_id: 'claude-sonnet-5-5', effort: 'low', at: 1 }
    })
    mount()
    const lead = within(rows()[0])
    expect(lead.getByTestId('seat-icon').getAttribute('data-subagents')).toBe('true') // the dots + light
    expect(lead.getByTestId('seat-host')).toBeTruthy()
    expect(lead.getByText('title L')).toBeTruthy()
    expect(lead.getAllByTestId('model-icon-opus').length).toBeGreaterThan(0)
    expect(lead.getByTestId('team-panel-model').textContent).toBe('Opus')
    expect(lead.getByTestId('team-panel-effort').textContent).toBe('high')
    expect(lead.getByTestId('team-panel-ctx').textContent).toBe('42%')
    expect(lead.getByTestId('context-ring')).toBeTruthy()
    const a = within(rows()[1])
    expect(a.getByTestId('team-panel-model').textContent).toBe('Sonnet') // model_id of the context sample
    expect(a.getByTestId('team-panel-effort').textContent).toBe('low')
    expect(a.getByTestId('team-panel-ctx').textContent).toBe('7%')
  })

  it('header shows panelName and its tooltip "<name> (<label>)"', () => {
    scene({ teamName: 'Alpha Team', teamLabel: 'alpha' })
    mount()
    const name = screen.getByTestId('team-panel-name')
    expect(name.textContent).toBe('Alpha Team')
    expect(name.getAttribute('title')).toBe('Alpha Team (alpha)')
    expect(screen.getByTestId('team-panel-count').textContent).toContain('3')
  })

  it('missing model or context shows —, never 0', () => {
    scene()
    mount()
    for (const r of rows()) {
      const row = within(r)
      expect(row.getByTestId('team-panel-model').textContent).toBe('—')
      expect(row.getByTestId('team-panel-effort').textContent).toBe('—')
      expect(row.getByTestId('team-panel-ctx').textContent).toBe('—')
    }
    expect(area().textContent).not.toContain('0%')
  })

  it('new team starts full', () => {
    scene()
    mount()
    expect(screen.getByTestId('team-panel').getAttribute('data-mode')).toBe('full')
  })

  it('row click opens an unopened member', () => {
    scene()
    mount()
    const before = Object.keys(useTabStore.getState().tabs).length
    fireEvent.click(rows()[3]) // C has no tab
    const tabs = useTabStore.getState().tabs
    expect(Object.keys(tabs)).toHaveLength(before + 1)
    expect(Object.values(tabs).some((t) => t.layout.type === 'leaf' && t.layout.pane.content.kind === 'tmux-session' && t.layout.pane.content.cachedName === 'c-tm')).toBe(true)
  })

  it('row click on an opened member switches to its tab', () => {
    scene()
    mount()
    fireEvent.click(rows()[2]) // B
    expect(useTabStore.getState().activeTabId).toBe('mb')
  })

  it('drag reorders (setMemberOrder), the lead never joins the order', () => {
    scene()
    mount()
    fireEvent.dragStart(rows()[1], { dataTransfer: { setData: vi.fn(), effectAllowed: '' } }) // A
    fireEvent.dragOver(rows()[3], { dataTransfer: { dropEffect: '' } }) // over C
    fireEvent.drop(rows()[3], { dataTransfer: {} })
    expect(useTeamUiStore.getState().memberOrder[KEY]).toEqual(['B', 'A', 'C'])
    expect(rows().map((r) => r.getAttribute('data-session-id'))).toEqual(['L', 'B', 'A', 'C'])
  })

  it('the header switches to one-line, and the mode is per team in the store', () => {
    scene()
    mount()
    fireEvent.click(screen.getByTestId('team-panel-to-line'))
    expect(useTeamUiStore.getState().panelMode[KEY]).toBe('line')
    expect(screen.getByTestId('team-panel').getAttribute('data-mode')).toBe('line')
    fireEvent.click(screen.getByTestId('team-panel-to-full'))
    expect(screen.getByTestId('team-panel').getAttribute('data-mode')).toBe('full')
  })
})

describe('one-line mode', () => {
  it('one-line cells + name, wraps', () => {
    scene()
    readings((t) => { t.lead.model = 'claude-opus-5-5'; t.lead.context = { used_percentage: 30, window: 1, at: 1 } })
    act(() => useTeamUiStore.getState().setPanelMode(KEY, 'line'))
    mount()
    const cells = screen.getAllByTestId('team-panel-cell')
    expect(cells.map((c) => c.getAttribute('data-session-id'))).toEqual(['L', 'A', 'B', 'C'])
    expect(screen.getByTestId('team-panel-name').textContent).toBeTruthy()
    expect(screen.getByTestId('team-panel-cells').className).toContain('flex-wrap')
    expect(within(cells[0]).getByTestId('seat-icon').getAttribute('data-subagents')).toBe('false')
    expect(within(cells[0]).getByTestId('context-ring')).toBeTruthy()
    expect(within(cells[0]).getByTestId('model-icon-opus')).toBeTruthy()
    expect(within(cells[1]).getByTestId('model-icon-unknown')).toBeTruthy() // no reading: the dashed "?"
  })

  it('a cell click opens the seat', () => {
    scene()
    act(() => useTeamUiStore.getState().setPanelMode(KEY, 'line'))
    mount()
    fireEvent.click(screen.getAllByTestId('team-panel-cell')[2]) // B
    expect(useTabStore.getState().activeTabId).toBe('mb')
  })
})

describe('resize and enlarge', () => {
  const drag = (from: number, to: number, commit = true) => {
    fireEvent.mouseDown(screen.getByTestId('resize-hit'), { clientX: from })
    fireEvent.mouseMove(document, { clientX: to })
    if (commit) fireEvent.mouseUp(document)
  }

  it('resize is draft-then-commit: the store changes on mouseup only', () => {
    scene()
    mount()
    drag(500, 420, false) // the left edge moves left 80px: 312 -> 392
    expect(area().style.width).toBe('392px')
    expect(useTeamUiStore.getState().panel.width).toBe(312)
    fireEvent.mouseUp(document)
    expect(useTeamUiStore.getState().panel.width).toBe(392)
    expect(area().style.width).toBe('392px')
  })

  it('a drag cut short by the panel going away leaves no draft and commits nothing later', () => {
    scene()
    const { unmount } = mount()
    drag(500, 420, false)
    unmount()
    fireEvent.mouseMove(document, { clientX: 100 })
    fireEvent.mouseUp(document)
    expect(useTeamUiStore.getState().panel.width).toBe(312)
    expect(document.body.style.cursor).toBe('')
    mount()
    expect(area().style.width).toBe('312px')
  })

  it('enlarging in the middle of a drag drops the draft: nothing commits and the width is unchanged', () => {
    scene()
    mount()
    drag(500, 420, false)
    act(() => { useTeamUiStore.getState().setPanelExpanded(true) })
    fireEvent.mouseMove(document, { clientX: 100 })
    fireEvent.mouseUp(document)
    act(() => { useTeamUiStore.getState().setPanelExpanded(false) })
    expect(useTeamUiStore.getState().panel.width).toBe(312)
    expect(area().style.width).toBe('312px')
  })

  it('resize clamps to 280-720', () => {
    scene()
    mount()
    drag(500, -2000)
    expect(useTeamUiStore.getState().panel.width).toBe(720)
    drag(500, 5000)
    expect(useTeamUiStore.getState().panel.width).toBe(280)
  })

  it('a click on the edge without moving writes nothing', () => {
    scene()
    mount()
    const before = useTeamUiStore.getState()
    fireEvent.mouseDown(screen.getByTestId('resize-hit'), { clientX: 500 })
    fireEvent.mouseUp(document)
    expect(useTeamUiStore.getState().panel).toBe(before.panel)
  })

  it('both modes take the area\'s width', () => {
    scene()
    act(() => useTeamUiStore.getState().setPanelWidth(600))
    mount()
    expect(area().style.width).toBe('600px')
    fireEvent.click(screen.getByTestId('team-panel-to-line'))
    expect(area().style.width).toBe('600px')
  })

  it('enlarge toggles expanded and keeps the width apart', () => {
    scene()
    act(() => useTeamUiStore.getState().setPanelWidth(500))
    mount()
    expect(area().getAttribute('data-expanded')).toBe('false')
    fireEvent.click(screen.getByTestId('team-panel-expand'))
    expect(useTeamUiStore.getState().panel).toEqual({ width: 500, expanded: true })
    expect(area().getAttribute('data-expanded')).toBe('true')
    expect(screen.queryByTestId('resize-hit')).toBeNull() // nothing to resize while it fills the area
    fireEvent.click(screen.getByTestId('team-panel-expand'))
    expect(useTeamUiStore.getState().panel).toEqual({ width: 500, expanded: false })
    expect(area().style.width).toBe('500px')
  })

  it('expanded works from one-line mode too', () => {
    scene()
    act(() => useTeamUiStore.getState().setPanelMode(KEY, 'line'))
    mount()
    fireEvent.click(screen.getByTestId('team-panel-expand'))
    expect(useTeamUiStore.getState().panel.expanded).toBe(true)
  })
})

describe('the panel state lives in the store (tab-hosted rule)', () => {
  it('mode remembered per team and survives a reload', () => {
    scene()
    const first = mount()
    fireEvent.click(screen.getByTestId('team-panel-to-line'))
    act(() => { useTeamUiStore.getState().setPanelWidth(555); useTeamUiStore.getState().setPanelExpanded(true) })
    first.unmount()
    // A reload: the in-memory state is gone, the persisted copy is read back.
    const saved = localStorage.getItem(STORAGE)!
    act(() => useTeamUiStore.setState({ panelMode: {}, panel: { width: 312, expanded: false } }))
    localStorage.setItem(STORAGE, saved)
    act(() => { void useTeamUiStore.persist.rehydrate() })
    mount()
    expect(screen.getByTestId('team-panel').getAttribute('data-mode')).toBe('line')
    expect(area().getAttribute('data-expanded')).toBe('true')
    act(() => useTeamUiStore.getState().setPanelExpanded(false))
    expect(area().style.width).toBe('555px')
  })

  it('switch tabs away and back keeps the mode, the width and the view (real TabContent)', () => {
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
    scene()
    act(() => { useTeamUiStore.getState().setPanelMode(KEY, 'line'); useTeamUiStore.getState().setPanelWidth(480) })
    render(<Shell />)
    expect(screen.getByTestId('team-panel').getAttribute('data-mode')).toBe('line')
    setActive('x')
    expect(screen.queryByTestId('team-panel')).toBeNull()
    setActive('mb')
    expect(screen.getByTestId('team-panel').getAttribute('data-mode')).toBe('line')
    expect(area().style.width).toBe('480px')
    setActive('lead')
    expect(screen.getByTestId('team-panel').getAttribute('data-mode')).toBe('line')
  })
})
