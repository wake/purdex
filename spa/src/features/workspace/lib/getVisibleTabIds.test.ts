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

  it('returns all tabs from tabOrder when activeWorkspaceId is null', () => {
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
    expect(result).toEqual(['t1', 't2'])
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
