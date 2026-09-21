import { describe, it, expect } from 'vitest'
import { getVisibleTabIds } from './getVisibleTabIds'
import type { Workspace } from '../../../types/tab'

describe('getVisibleTabIds', () => {
  it('returns workspace tabs when active workspace exists', () => {
    const workspaces: Workspace[] = [
      { id: 'ws-1', name: 'WS1', tabs: ['t1', 't2'], activeTabId: 't1' },
    ]
    const tabs: Record<string, unknown> = { t1: {}, t2: {}, t3: {} }
    const result = getVisibleTabIds({
      tabs,
      tabOrder: ['t1', 't2', 't3'],
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
      workspaces,
      activeWorkspaceId: 'ws-1',
    })
    expect(result).toEqual(['t1'])
  })

  it('returns all tabs from tabOrder when 0 workspaces', () => {
    const result = getVisibleTabIds({
      tabs: { t1: {}, t2: {}, t3: {} },
      tabOrder: ['t1', 't2', 't3'],
      workspaces: [],
      activeWorkspaceId: null,
    })
    expect(result).toEqual(['t1', 't2', 't3'])
  })

  // There is no "Home" view of workspace-less tabs any more. `activeWorkspaceId === null` with workspaces is the
  // moment before adopt-standalone.ts re-points it; the bar falls back to every tab. (Was: standalone tabs only.)
  it('returns all tabs from tabOrder when activeWorkspaceId is null', () => {
    const workspaces: Workspace[] = [
      { id: 'ws-1', name: 'WS1', tabs: ['t1'], activeTabId: 't1' },
    ]
    const result = getVisibleTabIds({
      tabs: { t1: {}, t2: {} },
      tabOrder: ['t1', 't2'],
      workspaces,
      activeWorkspaceId: null,
    })
    expect(result).toEqual(['t1', 't2'])
  })

  it('returns all tabs from tabOrder when activeWorkspaceId is null, with multiple workspaces', () => {
    const workspaces: Workspace[] = [
      { id: 'ws-1', name: 'WS1', tabs: ['t1', 't2'], activeTabId: 't1' },
      { id: 'ws-2', name: 'WS2', tabs: ['t3'], activeTabId: 't3' },
    ]
    const result = getVisibleTabIds({
      tabs: { t1: {}, t2: {}, t3: {}, t4: {}, t5: {} },
      tabOrder: ['t1', 't2', 't3', 't4', 't5'],
      workspaces,
      activeWorkspaceId: null,
    })
    expect(result).toEqual(['t1', 't2', 't3', 't4', 't5'])
  })

  it('returns empty array when no tabs exist', () => {
    const result = getVisibleTabIds({
      tabs: {},
      tabOrder: [],
      workspaces: [],
      activeWorkspaceId: null,
    })
    expect(result).toEqual([])
  })
})
