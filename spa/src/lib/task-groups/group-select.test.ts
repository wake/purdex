// spa/src/lib/task-groups/group-select.test.ts — selecting a group from the sidebar and switching the sidebar view (task groups spec §6.2, §6.3). Real stores.
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { activateTab, groupExists, selectGroupFromSidebar, switchSidebarView, targetTabOfGroup } from './group-select'
import { startGroupSelection } from './selection'
import { currentGroupOrderCtx } from './current'
import { manualGroupKey, teamGroupKey, UNGROUPED } from './group-key'
import type { Tab } from '../../types/tab'
import { useTabStore } from '../../stores/useTabStore'
import { useGroupViewStore } from '../../stores/useGroupViewStore'
import { useManualGroupsStore } from '../../stores/useManualGroupsStore'
import { useLayoutStore } from '../../stores/useLayoutStore'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { useWorkspaceStore } from '../../features/workspace/store'

const A = manualGroupKey('aaaaaaaa')
const B = manualGroupKey('bbbbbbbb')
const tab = (id: string, group?: string, rank?: string): Tab => ({ id, kind: 'new-tab', locked: false, pinned: false, createdAt: 1, layout: { type: 'leaf', pane: { id: `${id}-p`, content: { kind: 'dashboard' } } }, ...(group ? { group } : {}), ...(rank ? { groupRank: rank } : {}) } as never)
const view = () => useGroupViewStore.getState()
const active = () => useTabStore.getState().activeTabId
let stop: () => void

beforeEach(() => {
  localStorage.clear()
  useTeamRosterStore.getState().reset()
  const g = (name: string) => ({ name, members: [], createdAt: 1 })
  useManualGroupsStore.setState({ order: ['aaaaaaaa', 'bbbbbbbb', 'eeeeeeee'], groups: { aaaaaaaa: g('A'), bbbbbbbb: g('B'), eeeeeeee: g('Empty') } })
  useTabStore.setState({
    tabs: { a1: tab('a1', A, 'a'), a2: tab('a2', A, 'b'), b1: tab('b1', B), u1: tab('u1') },
    activeTabId: 'u1', visitHistory: [],
  } as never)
  useWorkspaceStore.setState({
    workspaces: [{ id: 'w1', name: 'W1', tabs: ['a1', 'a2'], activeTabId: 'a1', moduleConfig: {} }, { id: 'w2', name: 'W2', tabs: ['b1', 'u1'], activeTabId: 'u1', moduleConfig: {} }],
    activeWorkspaceId: 'w2',
  } as never)
  useGroupViewStore.setState({ selectedGroup: null, lastActiveTab: {}, expanded: {}, lastByView: { workspace: null, group: null } })
  useTeamUiStore.setState({ sidebarView: 'groups' })
  useLayoutStore.setState({ activityBarWidth: 'wide', tabPosition: 'top' })
  stop = startGroupSelection()
})
afterEach(() => { stop() })

describe('targetTabOfGroup', () => {
  it('the last active tab while it still belongs to the group, else the first tab, else none', () => {
    const ctx = currentGroupOrderCtx()
    expect(targetTabOfGroup(A, ctx)).toBe('a1')
    useGroupViewStore.setState({ lastActiveTab: { [A]: 'a2' } })
    expect(targetTabOfGroup(A, ctx)).toBe('a2')
    useGroupViewStore.setState({ lastActiveTab: { [A]: 'b1' } }) // a tab of another group, or one that is gone
    expect(targetTabOfGroup(A, ctx)).toBe('a1')
    useGroupViewStore.setState({ lastActiveTab: { [A]: 'gone' } })
    expect(targetTabOfGroup(A, ctx)).toBe('a1')
    expect(targetTabOfGroup(manualGroupKey('eeeeeeee'), ctx)).toBeNull()
  })

  it('groupExists: 「未分組」 always, a manual group by the store, a team by the index', () => {
    const ctx = currentGroupOrderCtx()
    expect([groupExists(UNGROUPED, ctx), groupExists(A, ctx), groupExists(manualGroupKey('cccccccc'), ctx), groupExists(teamGroupKey('h1\u0000t'), ctx), groupExists('junk', ctx)]).toEqual([true, true, false, false, false])
  })
})

describe('selectGroupFromSidebar', () => {
  it('activates the group\'s last tab and its workspace; the selection is the group', () => {
    useGroupViewStore.setState({ lastActiveTab: { [A]: 'a2' } })
    selectGroupFromSidebar(A)
    expect(active()).toBe('a2')
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe('w1')
    expect([view().selectedGroup, view().lastByView.group]).toEqual([A, A])
  })

  it('with no last tab it takes the first tab of the group', () => {
    selectGroupFromSidebar(B)
    expect(active()).toBe('b1')
    expect(view().selectedGroup).toBe(B)
  })

  it('an empty group is selected and the active tab becomes none', () => {
    const empty = manualGroupKey('eeeeeeee')
    selectGroupFromSidebar(empty)
    expect(active()).toBeNull()
    expect([view().selectedGroup, view().lastByView.group]).toEqual([empty, empty])
  })

  it('a group that does not exist changes nothing', () => {
    const before = view().selectedGroup // the group of the tab active when the subscriber started
    selectGroupFromSidebar(manualGroupKey('cccccccc'))
    expect(active()).toBe('u1')
    expect(view().selectedGroup).toBe(before)
  })
})

describe('switchSidebarView — restores the view\'s last place (spec §6.3)', () => {
  it('to the task-group view: the last group and its last tab', () => {
    useTeamUiStore.setState({ sidebarView: 'workspace' })
    useGroupViewStore.setState({ lastByView: { workspace: 'w2', group: A }, lastActiveTab: { [A]: 'a2' } })
    switchSidebarView('groups')
    expect(useTeamUiStore.getState().sidebarView).toBe('groups')
    expect([view().selectedGroup, active()]).toEqual([A, 'a2'])
  })

  it('to the task-group view when that group is gone: the group of the current tab', () => {
    useTeamUiStore.setState({ sidebarView: 'workspace' })
    useGroupViewStore.setState({ lastByView: { workspace: null, group: manualGroupKey('cccccccc') } })
    useTabStore.setState({ activeTabId: 'b1' } as never)
    switchSidebarView('groups')
    expect([view().selectedGroup, active()]).toEqual([B, 'b1'])
  })

  it('to the workspace view: the last workspace and its active tab', () => {
    useGroupViewStore.setState({ lastByView: { workspace: 'w1', group: A } })
    switchSidebarView('workspace')
    expect(useTeamUiStore.getState().sidebarView).toBe('workspace')
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe('w1')
    expect(active()).toBe('a1') // w1's active tab
  })

  it('to the workspace view when that workspace is gone: the workspace of the current tab', () => {
    useGroupViewStore.setState({ lastByView: { workspace: 'gone', group: A } })
    useTabStore.setState({ activeTabId: 'a2' } as never)
    switchSidebarView('workspace')
    expect(view().lastByView.workspace).toBe('w1')
    expect(active()).toBe('a2')
  })
})

describe('activateTab', () => {
  it('makes the tab and its workspace active, and the workspace remembers the tab', () => {
    activateTab('a2')
    expect(active()).toBe('a2')
    const w1 = useWorkspaceStore.getState().workspaces.find((w) => w.id === 'w1')!
    expect([useWorkspaceStore.getState().activeWorkspaceId, w1.activeTabId]).toEqual(['w1', 'a2'])
  })
})
