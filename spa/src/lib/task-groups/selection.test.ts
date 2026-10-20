// spa/src/lib/task-groups/selection.test.ts — the selection follows the active tab (task groups spec §6.2): in the task-group view the selected group, that group's last tab and the view's last group; in the workspace view the
// view's last workspace; nothing for a watcher-area tab or none. Real stores.
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { startGroupSelection, shouldResync } from './selection'
import { useTabStore } from '../../stores/useTabStore'
import { useGroupViewStore } from '../../stores/useGroupViewStore'
import { useManualGroupsStore } from '../../stores/useManualGroupsStore'
import { useLayoutStore } from '../../stores/useLayoutStore'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { manualGroupKey, UNGROUPED } from './group-key'
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
