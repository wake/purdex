// spa/src/features/workspace/components/InlineTabList.team.test.tsx — the team beads in the left tab list (team interface
// plan TI-3, spec §4.3, §4.6, P4, P7–P10). Real stores and the real TeamDisplayProvider throughout.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, fireEvent, render, screen, within } from '@testing-library/react'
import { DndContext } from '@dnd-kit/core'
import { InlineTabList } from './InlineTabList'
import { WorkspaceRow } from './WorkspaceRow'
import { TeamDisplayProvider } from '../../../components/team/TeamDisplayProvider'
import { startTeamTabLifecycle } from '../../../lib/team/team-tab-lifecycle'
import { useAgentStore } from '../../../stores/useAgentStore'
import { useHostStore } from '../../../stores/useHostStore'
import { useLayoutStore } from '../../../stores/useLayoutStore'
import { useShownHostsStore } from '../../../stores/useShownHostsStore'
import { useTabStore } from '../../../stores/useTabStore'
import { useTeamRosterStore } from '../../../stores/useTeamRosterStore'
import { useTeamUiStore } from '../../../stores/useTeamUiStore'
import { useUndoToast } from '../../../stores/useUndoToast'
import { useWorkspaceStore } from '../store'
import { HOST, KEY, member, resetTeamStores, seedScene, tabShowing, wsTabs } from '../../../lib/team/__tests__/team-fixture'

const members: Array<[string, string]> = [['A', 'a-tm'], ['B', 'b-tm'], ['C', 'c-tm']]
const base = { members, tabs: [['lead', 'lead-tm'], ['ma', 'a-tm'], ['mc', 'c-tm'], ['plain', null]] as Array<[string, string | null]>, workspaces: [{ id: 'w1', tabs: ['lead', 'ma', 'mc', 'plain'] }, { id: 'w2', tabs: [] }] }

function List({ ws = 'w1', active = null }: { ws?: string; active?: string | null }) {
  const tabs = useTabStore((s) => s.tabs)
  const ids = useWorkspaceStore((s) => s.workspaces.find((w) => w.id === ws)?.tabs ?? [])
  return (
    <InlineTabList
      tabIds={ids} tabsById={tabs} activeTabId={active} sourceWsId={ws}
      onSelect={() => {}} onClose={() => {}} onMiddleClick={() => {}} onContextMenu={() => {}}
    />
  )
}
const mount = (ui = <List />) => render(<DndContext><TeamDisplayProvider>{ui}</TeamDisplayProvider></DndContext>)
const beadIds = () => screen.getAllByTestId('team-bead').map((b) => b.getAttribute('data-session-id'))
const bead = (id: string) => screen.getAllByTestId('team-bead').find((b) => b.getAttribute('data-session-id') === id)!
const dt = () => ({ dataTransfer: { setData: () => {}, effectAllowed: '', dropEffect: '' } })

// jsdom has no DragEvent: without one the synthetic event carries no clientX (the drop side would be undefined).
if (typeof (globalThis as { DragEvent?: unknown }).DragEvent === 'undefined') {
  ;(globalThis as { DragEvent?: unknown }).DragEvent = class DragEvent extends MouseEvent {}
}

beforeEach(() => {
  resetTeamStores()
  useShownHostsStore.setState({ ids: [HOST] })
  useAgentStore.setState({ statuses: {}, agentTypes: {}, unread: {}, subagents: {} })
  useHostStore.setState({ hosts: {} })
})
afterEach(() => vi.restoreAllMocks())

describe('InlineTabList — team beads', () => {
  it('member tabs are not listed as rows', () => {
    seedScene(base)
    mount()
    // The lead and the plain tab are rows; the two member tabs are not.
    expect(screen.getAllByTestId('inline-tab-row')).toHaveLength(2)
    expect(screen.getAllByTestId('team-lead-block')).toHaveLength(1)
  })

  it('a member whose lead is in another workspace is an ordinary row, the active one visible', () => {
    seedScene({ members, tabs: [['lead', 'lead-tm'], ['ma', 'a-tm']], workspaces: [{ id: 'w1', tabs: ['lead'] }, { id: 'w2', tabs: ['ma'] }] })
    mount(<><div data-testid="one"><List ws="w1" /></div><div data-testid="two"><List ws="w2" active="ma" /></div></>)
    const two = within(screen.getByTestId('two'))
    expect(two.getAllByTestId('inline-tab-row')).toHaveLength(1)
    expect(two.getByTestId('inline-tab-row').getAttribute('data-active')).toBe('true')
    expect(two.queryByTestId('team-lead-block')).toBeNull()
    expect(within(screen.getByTestId('one')).getAllByTestId('team-bead')).toHaveLength(3)
  })

  it('a locked member that survived the lead close is an ordinary row and the ghost row still lists it', () => {
    seedScene({ members, tabs: [['ma', 'a-tm'], ['plain', null]], workspaces: [{ id: 'w1', tabs: ['ma', 'plain'] }] })
    act(() => useTeamUiStore.getState().setGhostWorkspace(KEY, 'w1'))
    mount()
    expect(screen.getAllByTestId('inline-tab-row')).toHaveLength(2) // ma and plain
    expect(within(screen.getByTestId('team-ghost-lead')).getAllByTestId('team-bead')).toHaveLength(3)
  })

  it('a collapsed team whose lead tab is closed shows the collapsed line on the ghost; the toggle sticks; reopening keeps it', () => {
    seedScene({ members, tabs: [['plain', null]], workspaces: [{ id: 'w1', tabs: ['plain'] }] })
    act(() => {
      useTeamUiStore.getState().setGhostWorkspace(KEY, 'w1')
      useTeamUiStore.getState().setCollapsed(KEY, true)
    })
    mount()
    const ghost = within(screen.getByTestId('team-ghost-lead'))
    expect(ghost.queryByTestId('team-bead')).toBeNull()
    expect(ghost.getAllByTestId('team-fold-dot')).toHaveLength(3)
    fireEvent.click(ghost.getByTestId('team-sidebar-collapsed'))
    expect(useTeamUiStore.getState().collapsed[KEY]).toBeUndefined()
    expect(within(screen.getByTestId('team-ghost-lead')).getAllByTestId('team-bead')).toHaveLength(3) // stays expanded
    fireEvent.click(within(screen.getByTestId('team-ghost-lead')).getByTestId('team-beads')) // blank click folds again
    expect(useTeamUiStore.getState().collapsed[KEY]).toBe(true)
    expect(within(screen.getByTestId('team-ghost-lead')).queryByTestId('team-bead')).toBeNull()
    // Reopening the lead goes through openTeamSeat, which expands a collapsed group first (R10); the live block then
    // draws the one shared state (no stale copy on the ghost).
    fireEvent.click(within(screen.getByTestId('team-ghost-lead')).getByTitle(/tab closed/))
    expect(tabShowing('lead-tm')).toBeDefined()
    expect(useTeamUiStore.getState().collapsed[KEY]).toBeUndefined()
    expect(within(screen.getByTestId('team-lead-block')).getAllByTestId('team-bead')).toHaveLength(3)
  })

  it('label capsule above the lead row: text and tooltip from the label / name, no +N, click toggles the shared collapse', () => {
    seedScene({ ...base, teamLabel: '發版', teamName: 'Release train' })
    mount()
    const capsule = within(screen.getByTestId('team-lead-block')).getByTestId('team-group-label')
    expect(capsule.textContent).toBe('發版')
    expect(capsule.getAttribute('title')).toBe('Release train (發版)')
    // Above the lead's row.
    const block = screen.getByTestId('team-lead-block')
    expect(capsule.compareDocumentPosition(within(block).getAllByTestId('inline-tab-row')[0]) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    fireEvent.click(capsule)
    expect(useTeamUiStore.getState().collapsed[KEY]).toBe(true) // the top bar's state
    expect(screen.queryByTestId('team-group-hidden')).toBeNull() // no +N in the sidebar (the beads show the members)
    expect(screen.getByTestId('team-sidebar-collapsed')).toBeInTheDocument()
    fireEvent.click(screen.getByTestId('team-group-label'))
    expect(useTeamUiStore.getState().collapsed[KEY]).toBeUndefined()
    expect(screen.getAllByTestId('team-bead')).toHaveLength(3)
  })

  it('an empty label shows the lead title cut to 10 wide, the tooltip carrying the whole title', () => {
    seedScene(base)
    const t = useTeamRosterStore.getState().byHost[HOST][0]
    act(() => useTeamRosterStore.setState({ byHost: { [HOST]: [{ ...t, lead: { ...t.lead, title: 'abcdefghijklmnop' } }] } }))
    mount()
    const capsule = screen.getByTestId('team-group-label')
    expect(capsule.textContent).toBe('abcdefghi…')
    expect(capsule.getAttribute('title')).toContain('abcdefghijklmnop')
  })

  it('the ghost lead row has the same capsule, faded like the ghost; clicking it toggles collapse', () => {
    seedScene({ members, tabs: [['plain', null]], workspaces: [{ id: 'w1', tabs: ['plain'] }], teamLabel: '發版' })
    act(() => useTeamUiStore.getState().setGhostWorkspace(KEY, 'w1'))
    mount()
    const capsule = within(screen.getByTestId('team-ghost-lead')).getByTestId('team-group-label')
    expect(capsule.textContent).toBe('發版')
    expect(capsule.closest('[data-testid="team-sidebar-label"]')!.className).toMatch(/opacity-/)
    fireEvent.click(capsule)
    expect(useTeamUiStore.getState().collapsed[KEY]).toBe(true)
  })

  it('beads in team order, wrap to rows', () => {
    seedScene(base)
    act(() => useTeamUiStore.getState().setMemberOrder(KEY, ['C', 'A']))
    // Join order would be A B C; the person's order puts C first, B (not in the list) last.
    const tops: Record<string, number> = { C: 0, A: 0, B: 26 }
    vi.spyOn(HTMLElement.prototype, 'offsetTop', 'get').mockImplementation(function (this: HTMLElement) { return tops[this.getAttribute('data-session-id') ?? ''] ?? 0 })
    mount()
    expect(beadIds()).toEqual(['C', 'A', 'B']) // opened or not
    expect(screen.getByTestId('team-beads').className).toContain('flex-wrap')
    // Two bead rows: one tick for the upper row and the turning elbow for the last (P10), stem from the block's lower edge.
    expect(screen.getAllByTestId('team-hook-tick')).toHaveLength(1)
    expect(screen.getAllByTestId('team-hook-elbow')).toHaveLength(1)
    expect(screen.getByTestId('team-hook-elbow').style.borderBottomLeftRadius).toBe('3px')
  })

  it('bead light follows the agent store', () => {
    seedScene(base)
    mount()
    const light = () => within(bead('A')).getByTestId('tab-status-indicator').style.backgroundColor
    expect(within(bead('B')).queryByTestId('tab-status-indicator')).toBeNull()
    act(() => useAgentStore.setState({ agentTypes: { [`${HOST}:code-a-tm`]: 'claude-code' }, statuses: { [`${HOST}:code-a-tm`]: 'running' } }))
    const running = light()
    expect(running).not.toBe('')
    act(() => useAgentStore.setState({ statuses: { [`${HOST}:code-a-tm`]: 'waiting' } }))
    expect(light()).not.toBe(running)
    // The member without a tab gets its light too (keyed by host and session code, not by a tab).
    act(() => useAgentStore.setState({ agentTypes: { [`${HOST}:code-b-tm`]: 'claude-code' }, statuses: { [`${HOST}:code-b-tm`]: 'running' } }))
    expect(within(bead('B')).getByTestId('tab-status-indicator').style.backgroundColor).toBe(running)
  })

  it('a bead with a tab and one without render identically (no fade, no open mark - P8)', () => {
    seedScene(base)
    mount()
    const withTab = bead('A')
    const without = bead('B')
    expect(withTab.className).toBe(without.className)
    expect(withTab.getAttribute('style')).toBe(without.getAttribute('style'))
    expect(withTab.innerHTML).toBe(without.innerHTML)
    expect(screen.queryByTestId('team-bead-open')).toBeNull()
    expect(withTab.getAttribute('data-open')).toBeNull()
    expect(withTab.className).not.toMatch(/opacity/)
  })

  it('bead click opens / switches (R3)', () => {
    seedScene(base)
    mount()
    fireEvent.click(bead('A'))
    expect(useTabStore.getState().activeTabId).toBe('ma') // switched, no second tab
    expect(Object.values(useTabStore.getState().tabs).filter((t) => t.layout.type === 'leaf' && t.layout.pane.content.kind === 'tmux-session' && t.layout.pane.content.cachedName === 'a-tm')).toHaveLength(1)
    fireEvent.click(bead('B')) // no tab yet: opened after the group's last tab
    const opened = tabShowing('b-tm')!
    expect(opened).toBeDefined()
    expect(useTabStore.getState().activeTabId).toBe(opened)
    expect(wsTabs('w1').indexOf(opened)).toBe(wsTabs('w1').indexOf('mc') + 1)
  })

  it('bead drag reorders memberOrder and the top group follows (R5)', async () => {
    seedScene(base)
    const stop = startTeamTabLifecycle()
    mount()
    fireEvent.dragStart(bead('A'), dt())
    fireEvent.dragOver(bead('C'), { ...dt(), clientX: 0 })
    fireEvent.drop(bead('C'), { ...dt(), clientX: 0 })
    fireEvent.dragEnd(bead('A'))
    await act(async () => { await Promise.resolve() }) // the lifecycle subscriber reorders the tab list in a microtask
    // jsdom has no layout: the drop lands on the "after" half, so A goes behind C.
    expect(useTeamUiStore.getState().memberOrder[KEY]).toEqual(['B', 'C', 'A'])
    expect(beadIds()).toEqual(['B', 'C', 'A'])
    expect(wsTabs('w1')).toEqual(['lead', 'mc', 'ma', 'plain']) // the top group (the tab list) follows
    // Before the target: A dropped on the left half of B.
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({ left: 100, width: 20, right: 120, top: 0, bottom: 24, height: 24, x: 100, y: 0, toJSON: () => ({}) })
    fireEvent.dragStart(bead('A'), dt())
    fireEvent.drop(bead('B'), { ...dt(), clientX: 101 })
    fireEvent.dragEnd(bead('A'))
    await act(async () => { await Promise.resolve() })
    stop()
    expect(useTeamUiStore.getState().memberOrder[KEY]).toEqual(['A', 'B', 'C'])
    expect(wsTabs('w1')).toEqual(['lead', 'ma', 'mc', 'plain'])
  })

  it('drop outside the block snaps back', () => {
    seedScene({ ...base, workspaces: [{ id: 'w1', tabs: ['lead', 'ma', 'mc', 'plain'] }, { id: 'w2', tabs: [] }] })
    mount()
    fireEvent.dragStart(bead('A'), dt())
    fireEvent.dragOver(screen.getAllByTestId('inline-tab-row')[1], dt()) // another row
    fireEvent.drop(screen.getAllByTestId('inline-tab-row')[1], dt())
    fireEvent.dragEnd(bead('A'))
    expect(useTeamUiStore.getState().memberOrder[KEY]).toBeUndefined()
    expect(beadIds()).toEqual(['A', 'B', 'C'])
    expect(bead('A').className).not.toMatch(/opacity-30/) // the drag ended, nothing left dimmed
  })

  it('collapse line shows one light per member and expands on click', () => {
    seedScene(base)
    act(() => useTeamUiStore.getState().setCollapsed(KEY, true))
    act(() => useAgentStore.setState({ statuses: { [`${HOST}:code-a-tm`]: 'running' } }))
    mount()
    expect(screen.queryByTestId('team-beads')).toBeNull()
    const line = screen.getByTestId('team-sidebar-collapsed')
    expect(within(line).getAllByTestId('team-fold-dot')).toHaveLength(3)
    expect(within(line).getAllByTestId('team-fold-dot').map((d) => d.getAttribute('data-status'))).toEqual(['running', 'none', 'none'])
    fireEvent.click(line)
    expect(useTeamUiStore.getState().collapsed[KEY]).toBeUndefined()
    expect(screen.getAllByTestId('team-bead')).toHaveLength(3)
  })

  it('the collapsed line sits on a plate; the expanded block has none', () => {
    seedScene(base)
    mount()
    expect(screen.queryByTestId('team-sidebar-plate')).toBeNull()
    act(() => useTeamUiStore.getState().setCollapsed(KEY, true))
    const plate = screen.getByTestId('team-sidebar-plate')
    expect(within(plate).getByTestId('team-sidebar-collapsed')).toBeInTheDocument()
    expect(plate.className).toMatch(/rounded/)
    expect(plate.className).toMatch(/bg-/)
    fireEvent.click(within(plate).getByTestId('team-sidebar-collapsed')) // still expands
    expect(useTeamUiStore.getState().collapsed[KEY]).toBeUndefined()
    expect(screen.queryByTestId('team-sidebar-plate')).toBeNull()
  })

  it('tick / blank click collapses', () => {
    seedScene(base)
    mount()
    fireEvent.click(screen.getByTestId('team-hook'))
    expect(useTeamUiStore.getState().collapsed[KEY]).toBe(true)
    fireEvent.click(screen.getByTestId('team-sidebar-collapsed')) // expand again
    expect(useTeamUiStore.getState().collapsed[KEY]).toBeUndefined()
    fireEvent.click(screen.getByTestId('team-beads')) // the blank part of the bead area
    expect(useTeamUiStore.getState().collapsed[KEY]).toBe(true)
    // A click on a bead does not collapse (it opens, and an open expands).
    fireEvent.click(screen.getByTestId('team-sidebar-collapsed'))
    fireEvent.click(bead('A'))
    expect(useTeamUiStore.getState().collapsed[KEY]).toBeUndefined()
  })

  it('ghost row after the lead tab is closed shows its beads; lead click reopens the group; bead click opens that member', () => {
    seedScene({ members, tabs: [['ma', 'a-tm'], ['plain', null]], workspaces: [{ id: 'w1', tabs: ['ma', 'plain'] }, { id: 'w2', tabs: [] }] })
    act(() => useTeamUiStore.getState().setGhostWorkspace(KEY, 'w1'))
    mount(<><List ws="w1" /><div data-testid="second"><List ws="w2" /></div></>)
    const ghost = screen.getByTestId('team-ghost-lead')
    expect(within(ghost).getAllByTestId('team-bead')).toHaveLength(3)
    expect(within(screen.getByTestId('second')).queryByTestId('team-ghost-lead')).toBeNull() // only the workspace it was closed from
    fireEvent.click(within(ghost).getByTitle(/tab closed/))
    const lead = tabShowing('lead-tm')
    expect(lead).toBeDefined()
    expect(screen.queryByTestId('team-ghost-lead')).toBeNull()
    expect(screen.getAllByTestId('team-lead-block')).toHaveLength(1)
  })

  it('a bead click on the ghost row opens that member (the lead first)', () => {
    seedScene({ members, tabs: [['plain', null]], workspaces: [{ id: 'w1', tabs: ['plain'] }] })
    act(() => useTeamUiStore.getState().setGhostWorkspace(KEY, 'w1'))
    mount()
    fireEvent.click(within(screen.getByTestId('team-ghost-lead')).getAllByTestId('team-bead')[1])
    expect(tabShowing('b-tm')).toBeDefined()
    expect(tabShowing('lead-tm')).toBeDefined()
  })

  it('host icon follows the setting', () => {
    seedScene(base)
    useHostStore.setState({ hosts: { [HOST]: { id: HOST, name: 'H', ip: '1.1.1.1', port: 1, order: 0, icon: 'Desktop' } as never } })
    mount()
    expect(within(bead('A')).getByTestId('host-badge')).toBeInTheDocument()
    act(() => useTeamUiStore.getState().setTeamBeadHost(false))
    expect(within(bead('A')).queryByTestId('host-badge')).toBeNull()
  })

  it('top-only tab position renders no beads', () => {
    seedScene(base)
    useLayoutStore.setState({ tabPosition: 'top', workspaceExpanded: { w1: true } })
    const ws = useWorkspaceStore.getState().workspaces[0]
    render(
      <DndContext><TeamDisplayProvider>
        <WorkspaceRow
          workspace={ws} isActive tabsById={useTabStore.getState().tabs} activeTabId={null}
          onSelectWorkspace={() => {}} onSelectTab={() => {}} onCloseTab={() => {}} onMiddleClickTab={() => {}}
          onContextMenuTab={() => {}} onAddTabToWorkspace={() => {}}
        />
      </TeamDisplayProvider></DndContext>,
    )
    expect(screen.queryByTestId('team-bead')).toBeNull()
    expect(screen.queryByTestId('team-lead-block')).toBeNull()
    expect(screen.queryByTestId('team-group-label')).toBeNull()
  })

  it('without a provider the list renders exactly as before (member tabs are rows, no beads)', () => {
    seedScene(base)
    render(<DndContext><List /></DndContext>)
    expect(screen.getAllByTestId('inline-tab-row')).toHaveLength(4)
    expect(screen.queryByTestId('team-bead')).toBeNull()
    expect(screen.queryByTestId('team-group-label')).toBeNull()
  })

  it('a remote member bead with an unmapped host shows the neutral glyph and clicking it toasts without opening anything', () => {
    seedScene(base)
    const t = useTeamRosterStore.getState().byHost[HOST][0]
    act(() => useTeamRosterStore.setState({ byHost: { [HOST]: [{ ...t, members: [...t.members, { ...member('R', 9, 'r-tm'), host_id: 'dm-b', host_alias: 'b26' }] }] } }))
    mount()
    const r = bead('R')
    expect(within(r).getByTestId('team-bead-host-unknown')).toBeInTheDocument()
    expect(within(bead('A')).queryByTestId('team-bead-host-unknown')).toBeNull()
    const tabsBefore = Object.keys(useTabStore.getState().tabs).length
    fireEvent.click(r)
    expect(Object.keys(useTabStore.getState().tabs)).toHaveLength(tabsBefore)
    expect(useUndoToast.getState().toast?.message).toContain('b26')
  })

  it('an unmapped-host bead always draws the bot icon; the neutral glyph only with the setting on', () => {
    seedScene(base)
    const t = useTeamRosterStore.getState().byHost[HOST][0]
    act(() => useTeamRosterStore.setState({ byHost: { [HOST]: [{ ...t, members: [...t.members, { ...member('R', 9, 'r-tm'), host_id: 'dm-b', host_alias: 'b26' }] }] } }))
    act(() => useTeamUiStore.getState().setTeamBeadHost(false))
    mount()
    const r = bead('R')
    expect(within(r).getByTestId('team-bead-bot')).toBeInTheDocument() // visible, not an empty click target
    expect(within(r).queryByTestId('tab-status-indicator')).toBeNull() // no agent store key, so no light
    expect(within(r).queryByTestId('team-bead-host-unknown')).toBeNull()
    expect(r.getAttribute('title')).toBe('title R')
    act(() => useTeamUiStore.getState().setTeamBeadHost(true))
    expect(within(bead('R')).getByTestId('team-bead-bot')).toBeInTheDocument()
    expect(within(bead('R')).getByTestId('team-bead-host-unknown')).toBeInTheDocument()
  })

  it("a joining member's tooltip carries the state, alias only when remote", () => {
    seedScene(base)
    const t = useTeamRosterStore.getState().byHost[HOST][0]
    const joiningLocal = { ...member('J', 8, 'j-tm'), state: 'joining' }
    const killingRemote = { ...member('K', 9, 'k-tm'), state: 'killing', host_id: 'dm-b', host_alias: 'b26' }
    act(() => useTeamRosterStore.setState({ byHost: { [HOST]: [{ ...t, members: [...t.members, joiningLocal, killingRemote] }] } }))
    mount()
    expect(bead('A').getAttribute('title')).toBe('title A')
    expect(bead('J').getAttribute('title')).toBe('title J · joining')
    expect(bead('K').getAttribute('title')).toBe('title K · b26: killing')
  })
})
