import { describe, it, expect } from 'vitest'
import { getVisibleTabIds } from './getVisibleTabIds'
import type { Workspace } from '../../../types/tab'
import { WATCH_WORKSPACE_ID } from '../../../lib/watch/ids'

describe('getVisibleTabIds', () => {
  it('returns workspace tabs when active workspace exists', () => {
    const workspaces: Workspace[] = [
      { id: 'ws-1', name: 'WS1', tabs: ['t1', 't2'], activeTabId: 't1' },
    ]
    const tabs: Record<string, unknown> = { t1: {}, t2: {}, t3: {} }
    const result = getVisibleTabIds({
      tabs,
      tabOrder: ['t1', 't2', 't3'],
      activeTabId: null,
      workspaces,
      activeWorkspaceId: 'ws-1',
    })
    expect(result).toEqual(['t1', 't2'])
  })

  it('filters out tabs not in tab store', () => {
    const workspaces: Workspace[] = [
      { id: 'ws-1', name: 'WS1', tabs: ['t1', 't2', 't3'], activeTabId: 't1' },
    ]
    const tabs: Record<string, unknown> = { t1: {}, t3: {} }
    const result = getVisibleTabIds({
      tabs,
      tabOrder: ['t1', 't3'],
      activeTabId: null,
      workspaces,
      activeWorkspaceId: 'ws-1',
    })
    expect(result).toEqual(['t1', 't3'])
  })

  // Every tab belongs to a workspace (Profile Sync spec §4.3). A tab nobody has adopted yet (adopt-standalone.ts
  // waits 500 ms) does not replace the bar. (Was: "returns only standalone tab when active tab is standalone".)
  it('an active tab that is in no workspace yet does not replace the bar: the active workspace\'s tabs', () => {
    const workspaces: Workspace[] = [
      { id: 'ws-1', name: 'WS1', tabs: ['t1'], activeTabId: 't1' },
    ]
    const tabs: Record<string, unknown> = { t1: {}, t2: {} }
    const result = getVisibleTabIds({
      tabs,
      tabOrder: ['t1', 't2'],
      activeTabId: null,
      workspaces,
      activeWorkspaceId: 'ws-1',
    })
    expect(result).toEqual(['t1'])
  })

  it('returns all tabs from tabOrder when 0 workspaces', () => {
    const result = getVisibleTabIds({
      tabs: { t1: {}, t2: {}, t3: {} },
      tabOrder: ['t1', 't2', 't3'],
      activeTabId: null,
      workspaces: [],
      activeWorkspaceId: null,
    })
    expect(result).toEqual(['t1', 't2', 't3'])
  })

  // `activeWorkspaceId === null` with workspaces is the moment before adopt-standalone.ts re-points it (boot, or
  // an unsettled world). The set is what close-others / close-right act on, so it must never be every tab: it
  // is what the bar will show once the pointer is back — the active tab's workspace, else the first.
  describe('no active workspace, but workspaces exist', () => {
    const workspaces: Workspace[] = [
      { id: 'ws-1', name: 'WS1', tabs: ['a1', 'a2'], activeTabId: 'a1' },
      { id: 'ws-2', name: 'WS2', tabs: ['b1', 'ghost', 'b2'], activeTabId: 'b1' },
    ]
    const tabs = { a1: {}, a2: {}, b1: {}, b2: {}, o: {} }
    const tabOrder = ['a1', 'b1', 'a2', 'b2', 'o']

    it('the active tab has an owner → that workspace\'s tabs (existing ones), never tabOrder', () => {
      expect(getVisibleTabIds({ tabs, tabOrder, activeTabId: 'b2', workspaces, activeWorkspaceId: null })).toEqual(['b1', 'b2'])
    })

    it('the active tab is in no workspace → the first workspace\'s tabs', () => {
      expect(getVisibleTabIds({ tabs, tabOrder, activeTabId: 'o', workspaces, activeWorkspaceId: null })).toEqual(['a1', 'a2'])
    })

    it('no active tab → the first workspace\'s tabs', () => {
      expect(getVisibleTabIds({ tabs, tabOrder, activeTabId: null, workspaces, activeWorkspaceId: null })).toEqual(['a1', 'a2'])
    })

    it('a pointer at a workspace that is gone is no pointer', () => {
      expect(getVisibleTabIds({ tabs, tabOrder, activeTabId: 'b1', workspaces, activeWorkspaceId: 'gone' })).toEqual(['b1', 'b2'])
    })

    it('an active workspace wins over the active tab\'s owner (the user is looking at ws-1\'s bar)', () => {
      expect(getVisibleTabIds({ tabs, tabOrder, activeTabId: 'b1', workspaces, activeWorkspaceId: 'ws-1' })).toEqual(['a1', 'a2'])
    })
  })

  it('returns empty array when no tabs exist', () => {
    const result = getVisibleTabIds({
      tabs: {},
      tabOrder: [],
      activeTabId: null,
      workspaces: [],
      activeWorkspaceId: null,
    })
    expect(result).toEqual([])
  })
})

describe('getVisibleTabIds — the task-group view\'s range (TG-4a)', () => {
  const workspaces: Workspace[] = [
    { id: 'ws-1', name: 'WS1', tabs: ['t1', 't2'], activeTabId: 't1' },
    { id: 'ws-2', name: 'WS2', tabs: ['t3'], activeTabId: 't3' },
  ]
  const base = { tabs: { t1: {}, t2: {}, t3: {} } as Record<string, unknown>, tabOrder: ['t1', 't2', 't3'], activeTabId: 't1', workspaces, activeWorkspaceId: 'ws-1' }

  it('a group range replaces the workspace: the selected group\'s tabs, in the range\'s order, across workspaces', () => {
    expect(getVisibleTabIds({ ...base, groupRange: ['t3', 't1'] })).toEqual(['t3', 't1'])
  })

  it('tabs the store no longer has are left out of the range', () => {
    expect(getVisibleTabIds({ ...base, groupRange: ['t3', 'gone', 't2'] })).toEqual(['t3', 't2'])
  })

  it('an empty selected group is an empty range, not the workspace\'s tabs', () => {
    expect(getVisibleTabIds({ ...base, groupRange: [] })).toEqual([])
  })

  it('no range (null / absent): the workspace rule', () => {
    expect(getVisibleTabIds({ ...base, groupRange: null })).toEqual(['t1', 't2'])
    expect(getVisibleTabIds(base)).toEqual(['t1', 't2'])
  })

  it('the watcher area keeps priority: in it the range is ignored', () => {
    const w: Workspace[] = [...workspaces, { id: WATCH_WORKSPACE_ID, name: 'watch', tabs: ['watch-jobs'], activeTabId: null }]
    expect(getVisibleTabIds({ ...base, workspaces: w, tabs: { ...base.tabs, 'watch-jobs': {} }, activeWorkspaceId: WATCH_WORKSPACE_ID, groupRange: ['t3'] })).toEqual(['watch-jobs'])
  })
})
