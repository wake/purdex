import { describe, it, expect } from 'vitest'
import { getVisibleTabIds } from './getVisibleTabIds'
import type { Workspace } from '../../../types/tab'

describe('getVisibleTabIds', () => {
  it('returns workspace tabs when active workspace exists', () => {
    const workspaces: Workspace[] = [
      { id: 'ws-1', name: 'WS1', color: '#aaa', tabs: ['t1', 't2'], activeTabId: 't1' },
    ]
    const tabs: Record<string, unknown> = { t1: {}, t2: {}, t3: {} }
    const result = getVisibleTabIds({
      tabs,
      tabOrder: ['t1', 't2', 't3'],
      activeTabId: 't1',
      workspaces,
      activeWorkspaceId: 'ws-1',
    })
    expect(result).toEqual(['t1', 't2'])
  })

  it('filters out tabs not in tab store', () => {
    const workspaces: Workspace[] = [
      { id: 'ws-1', name: 'WS1', color: '#aaa', tabs: ['t1', 't2', 't3'], activeTabId: 't1' },
    ]
    const tabs: Record<string, unknown> = { t1: {}, t3: {} }
    const result = getVisibleTabIds({
      tabs,
      tabOrder: ['t1', 't3'],
      activeTabId: 't1',
      workspaces,
      activeWorkspaceId: 'ws-1',
    })
    expect(result).toEqual(['t1', 't3'])
  })

  it('returns only standalone tab when active tab is standalone', () => {
    const workspaces: Workspace[] = [
      { id: 'ws-1', name: 'WS1', color: '#aaa', tabs: ['t1'], activeTabId: 't1' },
    ]
    const tabs: Record<string, unknown> = { t1: {}, t2: {} }
    const result = getVisibleTabIds({
      tabs,
      tabOrder: ['t1', 't2'],
      activeTabId: 't2',
      workspaces,
      activeWorkspaceId: 'ws-1',
    })
    expect(result).toEqual(['t2'])
  })

  it('returns all tabs from tabOrder when 0 workspaces', () => {
    const result = getVisibleTabIds({
      tabs: { t1: {}, t2: {}, t3: {} },
      tabOrder: ['t1', 't2', 't3'],
      activeTabId: 't1',
      workspaces: [],
      activeWorkspaceId: null,
    })
    expect(result).toEqual(['t1', 't2', 't3'])
  })

  it('returns only standalone tabs when activeWorkspaceId is null (Home mode)', () => {
    const workspaces: Workspace[] = [
      { id: 'ws-1', name: 'WS1', color: '#aaa', tabs: ['t1'], activeTabId: 't1' },
    ]
    const result = getVisibleTabIds({
      tabs: { t1: {}, t2: {} },
      tabOrder: ['t1', 't2'],
      activeTabId: null,
      workspaces,
      activeWorkspaceId: null,
    })
    // t1 belongs to ws-1, only t2 is standalone
    expect(result).toEqual(['t2'])
  })

  it('returns all standalone tabs in Home mode with multiple workspaces', () => {
    const workspaces: Workspace[] = [
      { id: 'ws-1', name: 'WS1', color: '#aaa', tabs: ['t1', 't2'], activeTabId: 't1' },
      { id: 'ws-2', name: 'WS2', color: '#bbb', tabs: ['t3'], activeTabId: 't3' },
    ]
    const result = getVisibleTabIds({
      tabs: { t1: {}, t2: {}, t3: {}, t4: {}, t5: {} },
      tabOrder: ['t1', 't2', 't3', 't4', 't5'],
      activeTabId: 't4',
      workspaces,
      activeWorkspaceId: null,
    })
    // t1,t2 in ws-1; t3 in ws-2; t4,t5 are standalone
    expect(result).toEqual(['t4', 't5'])
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
