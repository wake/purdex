// spa/src/lib/task-groups/selection.test.ts — the selection follows the active tab (task groups spec §6.2): in the task-group view the selected group, that group's last tab and the view's last group; in the workspace view the
// view's last workspace; nothing for a watcher-area tab or none. Real stores.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { startGroupSelection, shouldResync, TEAM_INDEX_SETTLE_MS } from './selection'
import { useTabStore } from '../../stores/useTabStore'
import { useGroupViewStore } from '../../stores/useGroupViewStore'
import { useManualGroupsStore } from '../../stores/useManualGroupsStore'
import { useLayoutStore } from '../../stores/useLayoutStore'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useRootCacheStore } from './root-cache'
import { HOST, KEY, resetTeamStores, seedScene } from '../team/__tests__/team-fixture'
import { useHostStore } from '../../stores/useHostStore'
import { useSessionStore } from '../../stores/useSessionStore'
import { useTeamRosterStore as rosterStore } from '../../stores/useTeamRosterStore'
import { manualGroupKey, teamGroupKey, UNGROUPED } from './group-key'
import type { Tab } from '../../types/tab'

const tab = (id: string, group?: string): Tab => ({ id, kind: 'new-tab', locked: false, pinned: false, createdAt: 1, layout: { type: 'leaf', pane: { id: `${id}-p`, content: { kind: 'new-tab' } } }, ...(group ? { group } : {}) } as never)
const G = manualGroupKey('aaaaaaaa')
const view = () => useGroupViewStore.getState()
let stop: () => void

beforeEach(() => {
  localStorage.clear()
  useTeamRosterStore.getState().reset()
  useManualGroupsStore.setState({ order: ['aaaaaaaa'], groups: { aaaaaaaa: { name: 'G', members: [], createdAt: 1 } } })
  useTabStore.setState({ tabs: { t1: tab('t1', G), t2: tab('t2'), 'watch-jobs': tab('watch-jobs') }, activeTabId: null, visitHistory: [] } as never)
  useWorkspaceStore.setState({ workspaces: [{ id: 'w1', name: 'W1', tabs: ['t1'], activeTabId: null, moduleConfig: {} }, { id: 'w2', name: 'W2', tabs: ['t2'], activeTabId: null, moduleConfig: {} }], activeWorkspaceId: 'w1' } as never)
  useGroupViewStore.setState({ selectedGroup: null, lastActiveTab: {}, expanded: {}, lastByView: { workspace: null, group: null } })
  useTeamUiStore.setState({ sidebarView: 'groups' })
  useLayoutStore.setState({ activityBarWidth: 'wide', tabPosition: 'top' })
  stop = startGroupSelection()
})
afterEach(() => { stop() })

describe('the selection subscriber — task-group view', () => {
  it('a click: the group of the new active tab is selected, its last tab and the view\'s last group follow', () => {
    useTabStore.getState().setActiveTab('t1')
    expect([view().selectedGroup, view().lastActiveTab, view().lastByView.group]).toEqual([G, { [G]: 't1' }, G])
    useTabStore.getState().setActiveTab('t2')
    expect([view().selectedGroup, view().lastActiveTab[UNGROUPED]]).toEqual([UNGROUPED, 't2'])
    expect(view().lastActiveTab[G]).toBe('t1') // the group left keeps its last tab
  })

  it('the active tab removed by an apply (the tab store drops it): the tab that becomes active is recorded', () => {
    useTabStore.getState().setActiveTab('t1')
    useTabStore.setState({ tabs: { t2: tab('t2') }, activeTabId: 't2' } as never)
    expect(view().selectedGroup).toBe(UNGROUPED)
  })

  it('a rehydrate from a second window that changes the active tab is followed', async () => {
    useTabStore.getState().setActiveTab('t2')
    const key = 'purdex-tabs'
    const raw = JSON.parse(localStorage.getItem(key)!)
    raw.state.activeTabId = 't1'
    raw.state.tabs = { t1: tab('t1', G), t2: tab('t2') }
    localStorage.setItem(key, JSON.stringify(raw))
    await useTabStore.persist.rehydrate()
    expect(useTabStore.getState().activeTabId).toBe('t1')
    expect(view().selectedGroup).toBe(G)
  })

  it('a watcher-area active tab and no active tab record nothing', () => {
    useTabStore.getState().setActiveTab('t1')
    const before = view()
    useTabStore.getState().setActiveTab('watch-jobs')
    expect(view().selectedGroup).toBe(G)
    useTabStore.getState().setActiveTab(null)
    expect(view().selectedGroup).toBe(G)
    expect(view().lastActiveTab).toBe(before.lastActiveTab)
  })

  it('in the narrow sidebar or with the tabs on the left, the workspace view records instead', () => {
    useLayoutStore.setState({ activityBarWidth: 'narrow' })
    useTabStore.getState().setActiveTab('t1')
    expect(view().selectedGroup).toBeNull()
    expect(view().lastByView).toEqual({ workspace: 'w1', group: null })
    useLayoutStore.setState({ activityBarWidth: 'wide', tabPosition: 'left' })
    useTabStore.getState().setActiveTab('t2')
    expect(view().selectedGroup).toBeNull()
    expect(view().lastByView.workspace).toBe('w2')
  })
})

describe('the selection subscriber — workspace view', () => {
  it('the view\'s last workspace becomes the active tab\'s workspace; the group view\'s memory is untouched', () => {
    useTeamUiStore.setState({ sidebarView: 'workspace' })
    useTabStore.getState().setActiveTab('t2')
    expect(view().lastByView).toEqual({ workspace: 'w2', group: null })
    expect(view().selectedGroup).toBeNull()
  })
})

describe('the selection subscriber — the watcher area in the workspace view', () => {
  it('a watcher-area active tab records no workspace either', () => {
    useTeamUiStore.setState({ sidebarView: 'workspace' })
    useWorkspaceStore.setState({ workspaces: [{ id: 'watch', name: 'Watch', tabs: ['watch-jobs'], activeTabId: null, moduleConfig: {} }, { id: 'w1', name: 'W1', tabs: ['t1'], activeTabId: null, moduleConfig: {} }] } as never)
    useTabStore.getState().setActiveTab('t1')
    useTabStore.getState().setActiveTab('watch-jobs')
    expect(view().lastByView.workspace).toBe('w1')
  })
})

describe('shouldResync', () => {
  it('is the single predicate: today only a change of the active tab', () => {
    const s = useTabStore.getState()
    expect(shouldResync({ ...s, activeTabId: 'b' }, { ...s, activeTabId: 'a' })).toBe(true)
    expect(shouldResync({ ...s, activeTabId: 'a' }, { ...s, activeTabId: 'a' })).toBe(false)
  })
})

describe('the active tab changes group (TG-5c, spec 6.2)', () => {
  const H = 'bbbbbbbb'
  const agentTab = (id: string): Tab => ({ ...tab(id), layout: { type: 'leaf', pane: { id: `${id}-p`, content: { kind: 'tmux-session', hostId: 'h1', sessionCode: 'c', mode: 'terminal', cachedName: 'n', tmuxInstance: 'i', rebuild: { agent: { sessionId: 'AG' } } } } } } as never)
  beforeEach(() => {
    useRootCacheStore.setState({ roots: { h1: { AG: 'ROOT' } }, addresses: {}, titles: {} })
    useManualGroupsStore.setState({
      order: ['aaaaaaaa', H],
      groups: { aaaaaaaa: { name: 'G', members: [{ host: 'h1', root: 'ROOT' }], createdAt: 1 }, [H]: { name: 'H', members: [], createdAt: 1 } },
    })
    useTabStore.setState({ tabs: { s1: agentTab('s1'), t2: tab('t2') }, activeTabId: null } as never)
    useTabStore.getState().setActiveTab('s1')
  })
  it('moved to another group (only the group store changes): the selection follows, the tab still active', () => {
    expect(view().selectedGroup).toBe(G)
    useManualGroupsStore.setState({
      groups: { aaaaaaaa: { name: 'G', members: [], createdAt: 1 }, [H]: { name: 'H', members: [{ host: 'h1', root: 'ROOT' }], createdAt: 1 } },
    })
    expect([view().selectedGroup, view().lastActiveTab[manualGroupKey(H)], useTabStore.getState().activeTabId]).toEqual([manualGroupKey(H), 's1', 's1'])
  })
  it('taken out: the selection moves to 「未分組」', () => {
    useManualGroupsStore.setState({ groups: { aaaaaaaa: { name: 'G', members: [], createdAt: 1 }, [H]: { name: 'H', members: [], createdAt: 1 } } })
    expect(view().selectedGroup).toBe(UNGROUPED)
  })
  it('its root arrives late (the cache changes): the selection follows to the group the root names', () => {
    useRootCacheStore.setState({ roots: {} })
    expect(view().selectedGroup).toBe(UNGROUPED)
    useRootCacheStore.setState({ roots: { h1: { AG: 'ROOT' } } })
    expect(view().selectedGroup).toBe(G)
  })
  it('a change that leaves the active tab\'s group alone records nothing new', () => {
    view().selectGroup(manualGroupKey(H)) // the person looks at another group
    useManualGroupsStore.setState({ order: ['aaaaaaaa', H], groups: { ...useManualGroupsStore.getState().groups, [H]: { name: 'H2', members: [], createdAt: 1 } } })
    expect(view().selectedGroup).toBe(manualGroupKey(H))
  })
  it('the workspace view does not select groups', () => {
    useTeamUiStore.setState({ sidebarView: 'workspace' })
    view().selectGroup(null)
    useManualGroupsStore.setState({ groups: { aaaaaaaa: { name: 'G', members: [], createdAt: 1 }, [H]: { name: 'H', members: [], createdAt: 1 } } })
    expect(view().selectedGroup).toBeNull()
  })
})

describe('the view in force and the team index (TG-5c)', () => {
  afterEach(() => { vi.useRealTimers() })
  const restart = (active: string) => {
    stop()
    resetTeamStores()
    seedScene({ members: [['A', 'a-tm']], tabs: [['lead', 'lead-tm'], ['ma', 'a-tm'], ['x', null]], workspaces: [{ id: 'w1', tabs: ['lead', 'ma', 'x'] }], activeTabId: active })
    useGroupViewStore.setState({ selectedGroup: null, lastActiveTab: {}, expanded: {}, lastByView: { workspace: null, group: null } })
    useTeamUiStore.setState({ sidebarView: 'groups' })
    useLayoutStore.setState({ activityBarWidth: 'wide', tabPosition: 'top' })
    stop = startGroupSelection()
  }
  it('a seat leaving the roster makes the active tab a plain tab: the selection follows once the burst is over', () => {
    vi.useFakeTimers()
    restart('ma')
    expect(view().selectedGroup).toBe(teamGroupKey(KEY))
    rosterStore.setState({ byHost: { [HOST]: [] } })
    vi.advanceTimersByTime(TEAM_INDEX_SETTLE_MS - 1)
    expect(view().selectedGroup).toBe(teamGroupKey(KEY))
    vi.advanceTimersByTime(1)
    expect(view().selectedGroup).toBe(UNGROUPED)
  })
  it('switching the view in force records nothing over what the toggle restores (spec 6.3)', () => {
    restart('ma')
    view().selectGroup(manualGroupKey('aaaaaaaa')) // what the group view restored
    useTeamUiStore.setState({ sidebarView: 'workspace' })
    useTeamUiStore.setState({ sidebarView: 'groups' })
    expect(view().selectedGroup).toBe(manualGroupKey('aaaaaaaa'))
    useLayoutStore.setState({ activityBarWidth: 'narrow' })
    useLayoutStore.setState({ activityBarWidth: 'wide' })
    expect(view().selectedGroup).toBe(manualGroupKey('aaaaaaaa'))
  })
  it('a remote seat\'s host being verified (host store only) makes the active tab the team\'s: the selection follows', () => {
    vi.useFakeTimers()
    restart('x')
    // member A lives on daemon-b = host h2; the active tab shows its session there
    rosterStore.setState({ byHost: { [HOST]: rosterStore.getState().byHost[HOST].map((t) => ({ ...t, members: t.members.map((m) => ({ ...m, host_id: 'daemon-b' })) })) } })
    useSessionStore.setState({ sessions: { ...useSessionStore.getState().sessions, h2: [{ code: 'code-a-tm', name: 'a-tm', mode: 'terminal', cwd: '~' }] as never } })
    const remote = { ...useTabStore.getState().tabs.ma, id: 'rm', layout: { type: 'leaf', pane: { id: 'p-rm', content: { kind: 'tmux-session', hostId: 'h2', sessionCode: 'code-a-tm', mode: 'terminal', cachedName: 'a-tm', tmuxInstance: 'i' } } } }
    useTabStore.setState((s) => ({ tabs: { ...s.tabs, rm: remote as never }, tabOrder: [...s.tabOrder, 'rm'] }))
    useHostStore.setState((s) => ({ hosts: { ...s.hosts, h2: { id: 'h2', name: 'h2', ip: '1.2.3.4', port: 1, daemonId: 'daemon-b' } as never } }))
    useTabStore.getState().setActiveTab('rm')
    vi.advanceTimersByTime(TEAM_INDEX_SETTLE_MS)
    expect(view().selectedGroup).toBe(UNGROUPED) // the host is not verified: not a seat
    useHostStore.setState((s) => ({ runtime: { ...s.runtime, h2: { status: 'connected', daemonIdVerified: { endpoint: '1.2.3.4:1', daemonId: 'daemon-b' } } as never } }))
    vi.advanceTimersByTime(TEAM_INDEX_SETTLE_MS)
    expect(view().selectedGroup).toBe(teamGroupKey(KEY))
  })
})
