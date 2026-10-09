// spa/src/lib/team/team-actions.test.ts — what a click on a team surface does (spec R3, R8–R11, §4.5; plan TI-1b). Real
// stores throughout.
import { describe, it, expect, beforeEach } from 'vitest'
import { openTeamSeat, toggleTeamCollapse, visibleTabIds } from './team-actions'
import { useTabStore } from '../../stores/useTabStore'
import { useHostStore } from '../../stores/useHostStore'
import { useSessionStore } from '../../stores/useSessionStore'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { currentTeamState } from './team-state'
import { member } from './__tests__/team-fixture'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { useUndoToast } from '../../stores/useUndoToast'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { usePaneFocusStore } from '../../stores/usePaneFocusStore'
import { tabOn } from './__tests__/team-fixture'
import { HOST, KEY, resetTeamStores, seedScene, tabShowing, wsTabs } from './__tests__/team-fixture'

beforeEach(() => {
  resetTeamStores()
  useShownHostsStore.setState({ ids: [HOST] })
})

const members: Array<[string, string]> = [['A', 'a-tm'], ['B', 'b-tm'], ['C', 'c-tm']]

describe('openTeamSeat', () => {
  it('opens an unopened member after the group\'s last tab in the lead\'s workspace', () => {
    seedScene({
      members,
      tabs: [['lead', 'lead-tm'], ['ma', 'a-tm'], ['mb', 'b-tm'], ['x', null], ['y', null]],
      workspaces: [{ id: 'w1', tabs: ['lead', 'x', 'ma', 'y', 'mb'] }, { id: 'w2', tabs: [] }],
      activeWorkspaceId: 'w2', // the active workspace is NOT the lead's
    })
    const r = openTeamSeat(KEY, 'C')
    expect(r.outcome).toBe('opened')
    expect(wsTabs('w1')).toEqual(['lead', 'x', 'ma', 'y', 'mb', r.tabId]) // after mb, the group's last tab
    expect(wsTabs('w2')).toEqual([])
    expect(useTabStore.getState().activeTabId).toBe(r.tabId)
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe('w1') // the workspace on screen follows
    expect(useTabStore.getState().tabs[r.tabId!].layout).toMatchObject({ pane: { content: { kind: 'tmux-session', hostId: 'h1', sessionCode: 'code-c-tm', cachedName: 'c-tm' } } })
  })

  it('switches to a member\'s existing tab (never a second)', () => {
    seedScene({ members, tabs: [['lead', 'lead-tm'], ['ma', 'a-tm'], ['mb', 'b-tm']], workspaces: [{ id: 'w1', tabs: ['lead', 'ma', 'mb'] }], activeTabId: 'lead' })
    const before = Object.keys(useTabStore.getState().tabs).length
    expect(openTeamSeat(KEY, 'B')).toEqual({ outcome: 'activated', tabId: 'mb' })
    expect(useTabStore.getState().activeTabId).toBe('mb')
    expect(Object.keys(useTabStore.getState().tabs)).toHaveLength(before)
  })

  it('switches to a seat\'s tab in another workspace, and the workspace on screen follows', () => {
    seedScene({ members, tabs: [['lead', 'lead-tm'], ['ma', 'a-tm']], workspaces: [{ id: 'w1', tabs: ['lead'] }, { id: 'w2', tabs: ['ma'] }], activeWorkspaceId: 'w1' })
    expect(openTeamSeat(KEY, 'A').outcome).toBe('activated')
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe('w2')
    expect(useTabStore.getState().activeTabId).toBe('ma')
  })

  it('reopens a closed lead first, in the ghost workspace, then the member after it', () => {
    seedScene({ members, tabs: [['x', null], ['y', null]], workspaces: [{ id: 'w1', tabs: ['x'] }, { id: 'w2', tabs: ['y'] }], activeWorkspaceId: 'w1' })
    useTeamUiStore.getState().setGhostWorkspace(KEY, 'w2')
    const r = openTeamSeat(KEY, 'A')
    expect(r.outcome).toBe('opened')
    const leadTab = tabShowing('lead-tm')!
    expect(wsTabs('w2')).toEqual(['y', leadTab, r.tabId]) // lead first in the ghost workspace, then the member
    expect(wsTabs('w1')).toEqual(['x'])
    expect(useTeamUiStore.getState().ghostWorkspace[KEY]).toBeUndefined() // the ghost entry is cleared
    expect(useTabStore.getState().activeTabId).toBe(r.tabId)
  })

  it('a lead with no tab and no remembered workspace reopens in the active workspace', () => {
    seedScene({ members, tabs: [['x', null]], workspaces: [{ id: 'w1', tabs: ['x'] }, { id: 'w2', tabs: [] }], activeWorkspaceId: 'w2' })
    const r = openTeamSeat(KEY, 'L')
    expect(r.outcome).toBe('opened')
    expect(wsTabs('w2')).toEqual([r.tabId])
  })

  it('expands a collapsed group before opening', () => {
    seedScene({ members, tabs: [['lead', 'lead-tm'], ['ma', 'a-tm']], workspaces: [{ id: 'w1', tabs: ['lead', 'ma'] }] })
    useTeamUiStore.getState().setCollapsed(KEY, true)
    expect(openTeamSeat(KEY, 'A').outcome).toBe('activated')
    expect(useTeamUiStore.getState().collapsed[KEY]).toBeUndefined()
    useTeamUiStore.getState().setCollapsed(KEY, true)
    expect(openTeamSeat(KEY, 'B').outcome).toBe('opened')
    expect(useTeamUiStore.getState().collapsed[KEY]).toBeUndefined()
  })

  it('an unlisted session opens no tab and says so (a toast), never a tab to nowhere', () => {
    seedScene({ members, tabs: [['lead', 'lead-tm']], workspaces: [{ id: 'w1', tabs: ['lead'] }], listed: ['lead-tm'] })
    const before = Object.keys(useTabStore.getState().tabs)
    const r = openTeamSeat(KEY, 'B')
    expect(r).toEqual({ outcome: 'unlisted', tabId: null })
    expect(Object.keys(useTabStore.getState().tabs)).toEqual(before)
    expect(useUndoToast.getState().toast?.message).toMatch(/session list/)
  })

  it('an unlisted LEAD stops a member from opening (nothing half-opened)', () => {
    seedScene({ members, tabs: [['x', null]], workspaces: [{ id: 'w1', tabs: ['x'] }], listed: ['a-tm'] })
    expect(openTeamSeat(KEY, 'A').outcome).toBe('unlisted')
    expect(Object.keys(useTabStore.getState().tabs)).toEqual(['x'])
  })

  it('a host hidden in this workbench opens nothing', () => {
    seedScene({ members, tabs: [['lead', 'lead-tm']], workspaces: [{ id: 'w1', tabs: ['lead'] }] })
    useShownHostsStore.setState({ ids: [] })
    expect(openTeamSeat(KEY, 'B')).toEqual({ outcome: 'hidden', tabId: null })
  })

  it('an unknown team or seat is a no-op', () => {
    seedScene({ members, tabs: [['lead', 'lead-tm']], workspaces: [{ id: 'w1', tabs: ['lead'] }] })
    expect(openTeamSeat('nope', 'A').outcome).toBe('unknown')
    expect(openTeamSeat(KEY, 'ZZZ').outcome).toBe('unknown')
  })
})

describe('openTeamSeat — a member on another host (TI-2a)', () => {
  const remoteMember = () => ({ ...member('R', 9, 'r-tm'), host_id: 'dm-b', host_alias: 'b26' })
  function seedRemote(mapped: boolean) {
    seedScene({ members: [['A', 'a-tm']], tabs: [['lead', 'lead-tm'], ['ma', 'a-tm']], workspaces: [{ id: 'w1', tabs: ['lead', 'ma'] }, { id: 'w2', tabs: [] }], activeWorkspaceId: 'w2' })
    const t = useTeamRosterStore.getState().byHost[HOST][0]
    useTeamRosterStore.setState({ byHost: { [HOST]: [{ ...t, members: [...t.members, remoteMember()] }] } })
    useHostStore.setState({
      hosts: (mapped ? { h2: { id: 'h2', name: 'b26', daemonId: 'dm-b' } } : {}) as never,
    })
    useSessionStore.setState({
      sessions: {
        ...useSessionStore.getState().sessions,
        h2: [{ code: 'code-r-tm', name: 'r-tm', mode: 'terminal', cwd: '~' }] as never,
      },
    })
    useShownHostsStore.setState({ ids: [HOST, 'h2'] })
  }

  it('a remote seat on a host this Mac lacks has hostId null, no tab, and openTeamSeat is a no-op with the toast', () => {
    seedRemote(false)
    useTeamUiStore.getState().setCollapsed(KEY, true)
    useTeamUiStore.getState().setGhostWorkspace(KEY, 'w2')
    const tabsBefore = Object.keys(useTabStore.getState().tabs)
    expect(currentTeamState().views[0].members.find((m) => m.session.session_id === 'R')).toMatchObject({ hostId: null, tabId: null })
    expect(openTeamSeat(KEY, 'R')).toEqual({ outcome: 'no-host', tabId: null })
    expect(Object.keys(useTabStore.getState().tabs)).toEqual(tabsBefore)
    expect(useUndoToast.getState().toast?.message).toContain('b26')
    expect(useTeamUiStore.getState().collapsed[KEY]).toBe(true) // collapse state untouched
    expect(useTeamUiStore.getState().ghostWorkspace[KEY]).toBe('w2')
  })

  it('a remote seat with a mapped host opens its tab on that host, after the group\'s last tab in the lead\'s workspace', () => {
    seedRemote(true)
    const r = openTeamSeat(KEY, 'R')
    expect(r.outcome).toBe('opened')
    expect(wsTabs('w1')).toEqual(['lead', 'ma', r.tabId])
    expect(useTabStore.getState().tabs[r.tabId!].layout).toMatchObject({ pane: { content: { kind: 'tmux-session', hostId: 'h2', sessionCode: 'code-r-tm', cachedName: 'r-tm' } } })
  })
})

describe('toggleTeamCollapse', () => {
  it('collapsing while a member tab is active activates the lead (R9)', () => {
    seedScene({ members, tabs: [['lead', 'lead-tm'], ['ma', 'a-tm']], workspaces: [{ id: 'w1', tabs: ['lead', 'ma'] }], activeTabId: 'ma' })
    toggleTeamCollapse(KEY)
    expect(useTeamUiStore.getState().collapsed[KEY]).toBe(true)
    expect(useTabStore.getState().activeTabId).toBe('lead')
  })

  it('collapsing while the lead or a non-team tab is active leaves the active tab alone; expanding too', () => {
    seedScene({ members, tabs: [['lead', 'lead-tm'], ['ma', 'a-tm'], ['x', null]], workspaces: [{ id: 'w1', tabs: ['lead', 'ma', 'x'] }], activeTabId: 'x' })
    toggleTeamCollapse(KEY)
    expect(useTabStore.getState().activeTabId).toBe('x')
    toggleTeamCollapse(KEY)
    expect(useTeamUiStore.getState().collapsed[KEY]).toBeUndefined()
    expect(useTabStore.getState().activeTabId).toBe('x')
  })
})

describe('visibleTabIds', () => {
  const hit = (key: string, role: 'lead' | 'member') => ({ key, role })
  const teamOf = (id: string) => ({ lead: hit('k', 'lead'), ma: hit('k', 'member'), mb: hit('k', 'member'), z: hit('other', 'member') } as Record<string, ReturnType<typeof hit>>)[id] ?? null
  it('drops the member tabs of collapsed teams only', () => {
    const ids = ['lead', 'ma', 'x', 'mb', 'z']
    expect(visibleTabIds(ids, { k: true }, teamOf)).toEqual(['lead', 'x', 'z'])
    expect(visibleTabIds(ids, {}, teamOf)).toEqual(ids)
    expect(visibleTabIds(ids, { k: true, other: true }, teamOf)).toEqual(['lead', 'x'])
  })
})

describe('the pane and the workspace follow (codex review of TI-1b)', () => {
  it('a seat shown in a SECONDARY pane of a split tab takes the keyboard, in the same workspace too', () => {
    seedScene({ members, tabs: [['lead', 'lead-tm'], ['x', null]], workspaces: [{ id: 'w1', tabs: ['lead', 'x'] }] })
    const a = tabOn('split', 'plain-tm')
    const b = tabOn('other', 'a-tm')
    useTabStore.setState((s) => ({
      tabs: {
        ...s.tabs,
        split: { ...a, layout: { type: 'split', id: 's1', direction: 'h', sizes: [1, 1], children: [a.layout, b.layout] } },
      },
      tabOrder: [...s.tabOrder, 'split'],
    }))
    useWorkspaceStore.setState((s) => ({ workspaces: s.workspaces.map((w) => ({ ...w, tabs: [...w.tabs, 'split'] })) }))
    usePaneFocusStore.setState({ recent: {}, focusRequest: null })
    expect(openTeamSeat(KEY, 'A')).toEqual({ outcome: 'activated', tabId: 'split' })
    expect(usePaneFocusStore.getState().focusRequest).toMatchObject({ paneId: 'p-other' }) // the secondary pane, not the primary
  })

  it('collapsing from a member in ANOTHER workspace brings the lead\'s workspace on screen with it', () => {
    seedScene({ members, tabs: [['lead', 'lead-tm'], ['ma', 'a-tm']], workspaces: [{ id: 'w1', tabs: ['lead'] }, { id: 'w2', tabs: ['ma'] }], activeWorkspaceId: 'w2', activeTabId: 'ma' })
    toggleTeamCollapse(KEY)
    expect(useTabStore.getState().activeTabId).toBe('lead')
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe('w1')
  })
})

describe('codex attack review of TI-1b', () => {
  it('collapsing from a member whose lead has no tab reopens the lead and shows it (the active tab is never hidden)', () => {
    seedScene({ members, tabs: [['ma', 'a-tm']], workspaces: [{ id: 'w1', tabs: ['ma'] }], activeTabId: 'ma' })
    toggleTeamCollapse(KEY)
    expect(useTeamUiStore.getState().collapsed[KEY]).toBe(true)
    const lead = tabShowing('lead-tm')
    expect(lead).toBeDefined()
    expect(useTabStore.getState().activeTabId).toBe(lead)
  })

  it('...and when the lead cannot be shown (not listed) the group stays open', () => {
    seedScene({ members, tabs: [['ma', 'a-tm']], workspaces: [{ id: 'w1', tabs: ['ma'] }], activeTabId: 'ma', listed: ['a-tm'] })
    toggleTeamCollapse(KEY)
    expect(useTeamUiStore.getState().collapsed[KEY]).toBeUndefined()
    expect(useTabStore.getState().activeTabId).toBe('ma')
  })

  it('a placement in a workspace that is gone falls back to the active workspace (no orphan tab)', async () => {
    seedScene({ members, tabs: [['x', null]], workspaces: [{ id: 'w1', tabs: ['x'] }] })
    const { openSessionTabAt } = await import('../open-session-tab')
    const id = openSessionTabAt('h1', { code: 'code-a-tm', name: 'a-tm', mode: 'terminal', cwd: '~' } as never, { workspaceId: 'gone', afterTabId: 'x' })
    expect(id).not.toBeNull()
    expect(useWorkspaceStore.getState().findWorkspaceByTab(id!)?.id).toBe('w1')
  })
})
