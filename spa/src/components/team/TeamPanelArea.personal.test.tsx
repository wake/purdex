// spa/src/components/team/TeamPanelArea.personal.test.tsx — task groups spec §7 (TG-5b): a tab of no team that shows an agent session gets the personal panel in the area's shared value.
// Real stores, the real provider and the real TabContent.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import { TabContent } from '../TabContent'
import { TeamDisplayProvider } from './TeamDisplayProvider'
import { TeamPanelArea } from './TeamPanelArea'
import { HOST, KEY, resetTeamStores, seedScene } from '../../lib/team/__tests__/team-fixture'
import { clearModuleRegistry, registerModule } from '../../lib/module-registry'
import { useTabStore } from '../../stores/useTabStore'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { useManualGroupsStore } from '../../stores/useManualGroupsStore'
import { setJournalHost } from '../../lib/journal/__tests__/journal-fixture'
import { seedWorkbook } from '../../lib/team/__tests__/workbook-fixture'
import { useWorkbookStore } from '../../stores/useWorkbookStore'
import { useRootCacheStore } from '../../lib/task-groups/root-cache'
import { usePersonaStore } from '../../stores/usePersonaStore'
import { makeCollection, makePersona } from '../../lib/names/__fixtures__/personas'
import { useI18nStore } from '../../stores/useI18nStore'
import { lineWidthFor } from './panel-layout'
import type { Tab } from '../../types/tab'

vi.mock('./TeamSeatIcon', () => ({
  TeamSeatIcon: (p: { light?: boolean }) => <span data-testid="seat-icon" data-light={String(p.light !== false)} />,
  TeamSeatHostBadge: () => <span data-testid="seat-host" />,
}))

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
const shared = (mode: 'titlebar' | 'line' | 'full') => act(() => { useTeamUiStore.getState().setSharedPanelMode(mode) })

function scene() {
  seedScene({ members: [['A', 'a-tm']], tabs: [['lead', 'lead-tm'], ['ma', 'a-tm'], ['x', null]], workspaces: [{ id: 'w1', tabs: ['lead', 'ma', 'x'] }], activeTabId: 'x' })
  addTab(ccTab('cc1', { type: 'cc', sessionId: 'S1' }))
}
const mount = () => render(<TeamDisplayProvider><TeamPanelArea /></TeamDisplayProvider>)
const fileInGroup = () => act(() => {
  useManualGroupsStore.setState({ order: ['g1'], groups: { g1: { name: 'iOS 發版', color: '#fb923c', members: [{ host: HOST, root: 'R' }], createdAt: 0 } } })
  useRootCacheStore.getState().ingest(HOST, [{ session_id: 'S1', root_session_id: 'R', address: 'mlab/purdex-emma-q9z', title: 'purdex-iface-tg5' }])
})

beforeEach(() => {
  cleanup()
  localStorage.clear()
  resetTeamStores()
  useManualGroupsStore.setState({ order: [], groups: {} })
  useWorkbookStore.getState().reset()
  useRootCacheStore.setState({ roots: {}, addresses: {}, titles: {} })
  usePersonaStore.setState({ byHost: {} })
  useI18nStore.getState().setLocale('zh-TW')
})
afterEach(() => cleanup())

describe('a tab that shows an agent session and is no team seat', () => {
  it('title bar: the area draws nothing in the pane (the strip in the title bar is the area)', () => {
    scene(); setActive('cc1'); mount()
    expect(screen.queryByTestId('team-panel-area')).toBeNull()
  })

  it('line: the group capsule, the session\'s cell and its name + title — the title alone when there is no persona (R15)', () => {
    scene(); setActive('cc1'); shared('line'); mount()
    expect(screen.getByTestId('personal-panel').getAttribute('data-mode')).toBe('line')
    expect(screen.getByTestId('team-panel-name').textContent).toBe('未分組')
    expect(screen.getByTestId('personal-cell')).toBeTruthy()
    expect(screen.getByTestId('personal-name').textContent).toBe('cc1')
    expect(screen.queryByTestId('personal-title')).toBeNull()
  })

  it('line: the width is a one-person team\'s (lead cell + 100 px), not the area\'s', () => {
    scene(); setActive('cc1'); shared('line'); mount()
    const area = useTeamUiStore.getState().panel.width
    const width = parseInt(screen.getByTestId('team-panel-area').style.width, 10)
    expect(width).toBe(lineWidthFor(1, area, 'badge'))
    expect(width).toBeLessThan(area)
    shared('full')
    expect(parseInt(screen.getByTestId('team-panel-area').style.width, 10)).toBe(area)
  })

  it('「未分組」 is neutral (no swatch colour, R24); a group the session\'s root is a member of is named on its colour', () => {
    scene(); setActive('cc1'); shared('line'); mount()
    const capsule = screen.getByTestId('team-panel-name')
    expect(capsule.getAttribute('data-group-kind')).toBe('ungrouped')
    expect(capsule.getAttribute('style') ?? '').not.toContain('background')
    fileInGroup()
    const named = screen.getByTestId('team-panel-name')
    expect(named.textContent).toBe('iOS 發版')
    expect(named.style.background).toBe('rgb(251, 146, 60)')
  })

  it('the persona\'s name comes from the address the root cache kept, with the title after it (an ended session keeps it)', () => {
    scene(); setActive('cc1'); shared('line'); mount()
    expect(screen.getByTestId('personal-name').textContent).toBe('cc1')
    const c = makeCollection(['q9z'])
    c.items[0] = makePersona('q9z', { name: 'Emma' })
    act(() => { usePersonaStore.setState({ byHost: { [HOST]: { status: 'ready', collection: c } } }) })
    fileInGroup() // the quota row's address ends in the persona's code
    expect(screen.getByTestId('personal-name').textContent).toBe('Emma')
    expect(screen.getByTestId('personal-title').textContent).toBe('purdex-iface-tg5') // the pdx title, not the tmux name
  })

  it('a click on the header toggles line <-> full; the ↑ moves the area to the title bar', () => {
    scene(); setActive('cc1'); shared('line'); mount()
    fireEvent.click(screen.getByTestId('team-panel-header'))
    expect(useTeamUiStore.getState().sharedPanelMode).toBe('full')
    expect(screen.getByTestId('personal-panel').getAttribute('data-mode')).toBe('full')
    fireEvent.click(screen.getByTestId('team-panel-to-titlebar'))
    expect(useTeamUiStore.getState().sharedPanelMode).toBe('titlebar')
  })

  it('full: the header carries the sessions of the group — none for 「未分組」', () => {
    scene(); setActive('cc1'); shared('full'); mount()
    expect(screen.queryByTestId('personal-count')).toBeNull()
    fileInGroup()
    expect(screen.getByTestId('personal-count').textContent).toContain('1 個 session')
  })

  it('a codex session counts; a pane with no recorded session id, and a settings-like tab, have none (R1)', () => {
    scene(); addTab(ccTab('cx', { type: 'codex', sessionId: 'S2' })); addTab(ccTab('cn', { type: 'cc' })); addTab(ccTab('plain', null))
    shared('full'); mount()
    setActive('cx'); expect(screen.queryByTestId('personal-panel')).not.toBeNull()
    setActive('cn'); expect(screen.queryByTestId('team-panel-area')).toBeNull()
    setActive('plain'); expect(screen.queryByTestId('team-panel-area')).toBeNull()
    setActive('x'); expect(screen.queryByTestId('team-panel-area')).toBeNull()
  })

  it('an ended session keeps its panel, dim (R2)', () => {
    scene(); setActive('cc1'); shared('line')
    act(() => {
      useTabStore.setState((s) => {
        const t = s.tabs.cc1
        if (t.layout.type !== 'leaf' || t.layout.pane.content.kind !== 'tmux-session') return s
        return { tabs: { ...s.tabs, cc1: { ...t, layout: { type: 'leaf', pane: { ...t.layout.pane, content: { ...t.layout.pane.content, terminated: 'tmux-restarted' } } } } } }
      })
    })
    mount()
    expect(screen.getByTestId('personal-cell').getAttribute('data-ended')).toBe('true')
    expect(screen.getByTestId('seat-icon').getAttribute('data-light')).toBe('false') // no light, not just dimmed
  })
})

describe('the status line asks for the panel\'s own session', () => {
  it('a codex session\'s workbook is loaded (not only a live cc pane\'s)', () => {
    const orig = useWorkbookStore.getState().loadSeat
    const loadSeat = vi.fn(() => Promise.resolve('ok'))
    act(() => { useWorkbookStore.setState((s) => ({ support: { ...s.support, [HOST]: { v1: true, v2: false } }, loadSeat }) as never) })
    scene(); addTab(ccTab('cx', { type: 'codex', sessionId: 'S2' })); setActive('cx'); shared('line'); mount()
    expect(loadSeat).toHaveBeenCalledWith(HOST, 'S2')
    act(() => { useWorkbookStore.setState({ loadSeat: orig } as never) })
  })
})

describe('the interim full form (until TG-5c)', () => {
  it('an older host\'s own workbook still opens in the full form; without one the personal panel shows', () => {
    scene(); seedWorkbook(HOST, 'S1', { status: 'Wiring the login form.', entries: [] })
    setActive('cc1'); shared('full'); mount()
    expect(screen.getByTestId('own-workbook')).toBeTruthy()
    expect(screen.queryByTestId('personal-panel')).toBeNull()
    shared('line')
    expect(screen.getByTestId('personal-panel').getAttribute('data-mode')).toBe('line')
  })
})

describe('the interim full form follows the panel\'s target', () => {
  it('on a journal.v1 host the workbook view stays off: the personal panel shows in full', () => {
    scene(); seedWorkbook(HOST, 'S1', { status: 'Wiring.', entries: [] })
    setJournalHost(HOST, true)
    setActive('cc1'); shared('full'); mount()
    expect(screen.queryByTestId('own-workbook')).toBeNull()
    expect(screen.getByTestId('personal-panel').getAttribute('data-mode')).toBe('full')
    setJournalHost(HOST, false)
  })

  it('a split tab whose first agent pane is codex and second is cc with a workbook: full does not switch to the cc workbook', () => {
    scene(); seedWorkbook(HOST, 'S1', { status: 'cc status', entries: [] })
    const cx = ccTab('sp', { type: 'codex', sessionId: 'S2' })
    const cc = ccTab('sp2', { type: 'cc', sessionId: 'S1' })
    addTab({ ...cx, layout: { type: 'split', direction: 'h', sizes: [50, 50], children: [cx.layout, cc.layout] } } as unknown as Tab)
    setActive('sp'); shared('full'); mount()
    expect(screen.queryByTestId('own-workbook')).toBeNull()
    expect(screen.getByTestId('personal-panel').getAttribute('data-mode')).toBe('full')
  })
})

describe('a team tab is still the team view', () => {
  it('shows the team whatever the shared value is, and the team value is the team\'s own', () => {
    scene(); shared('line')
    setActive('lead'); mount()
    expect(screen.getByTestId('team-panel')).toBeTruthy()
    expect(screen.queryByTestId('personal-panel')).toBeNull()
    expect(screen.getByTestId('team-panel').getAttribute('data-mode')).toBe('full')
    expect(KEY).toBeTruthy()
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
    scene(); setActive('cc1'); shared('full')
    render(<Shell />)
    expect(screen.getByTestId('personal-panel').getAttribute('data-mode')).toBe('full')
    setActive('x')
    expect(screen.queryByTestId('team-panel-area')).toBeNull()
    setActive('lead')
    expect(screen.getByTestId('team-panel').getAttribute('data-mode')).toBe('full')
    setActive('cc1')
    expect(screen.getByTestId('personal-panel').getAttribute('data-mode')).toBe('full')
    shared('line')
    setActive('x'); setActive('cc1')
    expect(screen.getByTestId('personal-panel').getAttribute('data-mode')).toBe('line')
  })
})
