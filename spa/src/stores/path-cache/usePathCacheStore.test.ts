import { describe, it, expect, beforeEach } from 'vitest'
import { usePathCacheStore } from './usePathCacheStore'

const reset = () =>
  usePathCacheStore.setState({ dirsByScope: {} } as never, false)

describe('usePathCacheStore', () => {
  beforeEach(reset)

  it('add inserts dir at head and dedups existing dir', () => {
    usePathCacheStore.getState().add('h1', 'w1', '/a/b')
    usePathCacheStore.getState().add('h1', 'w1', '/c/d')
    usePathCacheStore.getState().add('h1', 'w1', '/a/b')
    expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toEqual(['/a/b', '/c/d'])
  })

  it('LRU caps at 50 entries per scope', () => {
    for (let i = 0; i < 60; i++) usePathCacheStore.getState().add('h1', 'w1', `/d${i}`)
    const dirs = usePathCacheStore.getState().dirsByScope['h1:w1']
    expect(dirs.length).toBe(50)
    expect(dirs[0]).toBe('/d59')
    expect(dirs[49]).toBe('/d10')
  })

  it('LRU touches existing dir back to head + evicts tail on overflow', () => {
    for (let i = 0; i < 50; i++) usePathCacheStore.getState().add('h1', 'w1', `/d${i}`)
    usePathCacheStore.getState().add('h1', 'w1', '/d0')
    usePathCacheStore.getState().add('h1', 'w1', '/d50')
    const dirs = usePathCacheStore.getState().dirsByScope['h1:w1']
    expect(dirs.length).toBe(50)
    expect(dirs[0]).toBe('/d50')
    expect(dirs[1]).toBe('/d0')
    expect(dirs.includes('/d1')).toBe(false)
  })

  it('add silently rejects non-absolute path', () => {
    usePathCacheStore.getState().add('h1', 'w1', 'rel/path')
    expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toBeUndefined()
  })

  it('add normalizes trailing slash and ./ ..', () => {
    usePathCacheStore.getState().add('h1', 'w1', '/a/b/')
    usePathCacheStore.getState().add('h1', 'w1', '/a/./b')
    usePathCacheStore.getState().add('h1', 'w1', '/a/c/../b')
    expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toEqual(['/a/b'])
  })

  it('add silently rejects empty / non-string dir', () => {
    usePathCacheStore.getState().add('h1', 'w1', '')
    usePathCacheStore.getState().add('h1', 'w1', null as never)
    expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toBeUndefined()
  })

  it('lookup combines basename with each cached dir (head first)', () => {
    usePathCacheStore.getState().add('h1', 'w1', '/a/b')
    usePathCacheStore.getState().add('h1', 'w1', '/c/d')
    expect(usePathCacheStore.getState().lookup('h1', 'w1', 'foo.go')).toEqual([
      '/c/d/foo.go', '/a/b/foo.go',
    ])
  })

  it('lookup returns empty when scope has no cached dirs', () => {
    expect(usePathCacheStore.getState().lookup('h1', 'w1', 'foo.go')).toEqual([])
  })

  it('pruneStaleCandidate removes the dirname entry', () => {
    usePathCacheStore.getState().add('h1', 'w1', '/a/b')
    usePathCacheStore.getState().pruneStaleCandidate('h1', 'w1', '/a/b/foo.go')
    expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toEqual([])
  })

  it('pruneStaleCandidate is a no-op when dir not in cache', () => {
    usePathCacheStore.getState().add('h1', 'w1', '/a/b')
    usePathCacheStore.getState().pruneStaleCandidate('h1', 'w1', '/c/d/foo.go')
    expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toEqual(['/a/b'])
  })

  it('clearScope removes the named scope only', () => {
    usePathCacheStore.getState().add('h1', 'w1', '/a')
    usePathCacheStore.getState().add('h1', 'w2', '/b')
    usePathCacheStore.getState().add('h2', 'w1', '/c')
    usePathCacheStore.getState().clearScope('h1', 'w1')
    expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toBeUndefined()
    expect(usePathCacheStore.getState().dirsByScope['h1:w2']).toEqual(['/b'])
    expect(usePathCacheStore.getState().dirsByScope['h2:w1']).toEqual(['/c'])
  })

  it('clearHost removes all scopes for that host', () => {
    usePathCacheStore.getState().add('h1', 'w1', '/a')
    usePathCacheStore.getState().add('h2', 'w1', '/b')
    usePathCacheStore.getState().clearHost('h1')
    expect(usePathCacheStore.getState().dirsByScope['h1:w1']).toBeUndefined()
    expect(usePathCacheStore.getState().dirsByScope['h2:w1']).toEqual(['/b'])
  })
})
