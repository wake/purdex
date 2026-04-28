import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useHostStore } from '../useHostStore'
import { usePathCacheStore } from './usePathCacheStore'
import { attachPathCacheAutoCleanup } from './auto-cleanup'

let dispose: (() => void) | undefined

beforeEach(() => {
  usePathCacheStore.setState({ dirsByScope: {} } as never, false)
  useWorkspaceStore.setState({
    workspaces: [
      { id: 'w1', name: 'A', tabs: [], activeTabId: null, moduleConfig: {} },
      { id: 'w2', name: 'B', tabs: [], activeTabId: null, moduleConfig: {} },
    ],
    activeWorkspaceId: 'w1',
    _lastRemovedKeepSettings: undefined,
  } as never, false)
  useHostStore.setState({ hostOrder: ['h1', 'h2'] } as never, false)
  dispose = attachPathCacheAutoCleanup()
})

afterEach(() => {
  dispose?.()
  dispose = undefined
})

describe('attachPathCacheAutoCleanup', () => {
  it('returns a dispose function', () => {
    expect(typeof dispose).toBe('function')
  })

  it('workspace removal (real delete) clears in-memory cache for that workspace across all hosts', () => {
    usePathCacheStore.getState().add('h1', 'w1', '/a')
    usePathCacheStore.getState().add('h2', 'w1', '/x')
    usePathCacheStore.getState().add('h1', 'w2', '/b')
    useWorkspaceStore.setState({
      workspaces: [{ id: 'w2', name: 'B', tabs: [], activeTabId: null, moduleConfig: {} }],
      activeWorkspaceId: 'w2',
      _lastRemovedKeepSettings: undefined,
    } as never, false)
    expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toBeUndefined()
    expect(usePathCacheStore.getState().dirsByScope['h2:w1']).toBeUndefined()
    expect(usePathCacheStore.getState().dirsByScope['h1:w2']).toEqual(['/b'])
  })

  it('workspace tear-off (keepSettings:true) skips cleanup so in-memory + persisted cache survives', () => {
    usePathCacheStore.getState().add('h1', 'w1', '/a')
    useWorkspaceStore.setState({
      workspaces: [{ id: 'w2', name: 'B', tabs: [], activeTabId: null, moduleConfig: {} }],
      activeWorkspaceId: 'w2',
      _lastRemovedKeepSettings: 'w1',
    } as never, false)
    expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toEqual(['/a'])
  })

  it('host removal clears all scopes for that host', () => {
    usePathCacheStore.getState().add('h1', 'w1', '/a')
    usePathCacheStore.getState().add('h2', 'w1', '/b')
    useHostStore.setState({ hostOrder: ['h2'] } as never, false)
    expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toBeUndefined()
    expect(usePathCacheStore.getState().dirsByScope['h2:w1']).toEqual(['/b'])
  })

  it('after dispose, no longer cleans on workspace removal', () => {
    dispose?.()
    dispose = undefined
    usePathCacheStore.getState().add('h1', 'w1', '/a')
    useWorkspaceStore.setState({
      workspaces: [{ id: 'w2', name: 'B', tabs: [], activeTabId: null, moduleConfig: {} }],
      activeWorkspaceId: 'w2',
      _lastRemovedKeepSettings: undefined,
    } as never, false)
    expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toEqual(['/a'])
  })

  it('after dispose, no longer cleans on host removal', () => {
    dispose?.()
    dispose = undefined
    usePathCacheStore.getState().add('h1', 'w1', '/a')
    useHostStore.setState({ hostOrder: ['h2'] } as never, false)
    expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toEqual(['/a'])
  })

  it('non-removal workspace updates do not touch cache', () => {
    usePathCacheStore.getState().add('h1', 'w1', '/a')
    useWorkspaceStore.setState({
      workspaces: [
        { id: 'w1', name: 'A renamed', tabs: [], activeTabId: null, moduleConfig: {} },
        { id: 'w2', name: 'B', tabs: [], activeTabId: null, moduleConfig: {} },
      ],
      activeWorkspaceId: 'w1',
      _lastRemovedKeepSettings: undefined,
    } as never, false)
    expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toEqual(['/a'])
  })
})
