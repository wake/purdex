import { describe, it, expect, beforeEach } from 'vitest'
import { usePathCacheStore, sanitizeRehydratedPathCache } from './usePathCacheStore'
import { scopeKey } from './path-utils'

const reset = () =>
  usePathCacheStore.setState({ entriesByScope: {} } as never, false)

const dirsAt = (hostId: string, cwd: string) =>
  (usePathCacheStore.getState().entriesByScope[scopeKey(hostId, cwd)] ?? []).map((e) => e.dir)

describe('usePathCacheStore', () => {
  beforeEach(reset)

  it('add inserts dir at head and dedups within (host, cwd)', () => {
    usePathCacheStore.getState().add('h1', '/repo', 'sess1', '/repo/src')
    usePathCacheStore.getState().add('h1', '/repo', 'sess1', '/repo/lib')
    usePathCacheStore.getState().add('h1', '/repo', 'sess1', '/repo/src')
    expect(dirsAt('h1', '/repo')).toEqual(['/repo/src', '/repo/lib'])
  })

  it('LRU caps at 50 entries per (host, cwd)', () => {
    for (let i = 0; i < 60; i++) {
      usePathCacheStore.getState().add('h1', '/repo', 'sess1', `/repo/d${i}`)
    }
    const dirs = dirsAt('h1', '/repo')
    expect(dirs.length).toBe(50)
    expect(dirs[0]).toBe('/repo/d59')
    expect(dirs[49]).toBe('/repo/d10')
  })

  it('LRU touches existing dir back to head and evicts tail', () => {
    for (let i = 0; i < 50; i++) {
      usePathCacheStore.getState().add('h1', '/repo', 'sess1', `/repo/d${i}`)
    }
    usePathCacheStore.getState().add('h1', '/repo', 'sess1', '/repo/d0')
    usePathCacheStore.getState().add('h1', '/repo', 'sess1', '/repo/d50')
    const dirs = dirsAt('h1', '/repo')
    expect(dirs.length).toBe(50)
    expect(dirs[0]).toBe('/repo/d50')
    expect(dirs[1]).toBe('/repo/d0')
    expect(dirs.includes('/repo/d1')).toBe(false)
  })

  it('add silently rejects non-absolute / oversized / control-char dir', () => {
    usePathCacheStore.getState().add('h1', '/repo', 'sess1', 'rel/path')
    usePathCacheStore.getState().add('h1', '/repo', 'sess1', '')
    usePathCacheStore.getState().add('h1', '/repo', 'sess1', '/repo/' + 'x'.repeat(5000))
    usePathCacheStore.getState().add('h1', '/repo', 'sess1', '/repo/\x00null')
    expect(usePathCacheStore.getState().entriesByScope[scopeKey('h1', '/repo')]).toBeUndefined()
  })

  it('add silently rejects non-absolute / control-char cwd', () => {
    usePathCacheStore.getState().add('h1', 'rel/cwd', 'sess1', '/repo/src')
    usePathCacheStore.getState().add('h1', '/repo\x00x', 'sess1', '/repo/src')
    expect(Object.keys(usePathCacheStore.getState().entriesByScope)).toEqual([])
  })

  it('add normalizes trailing slash on cwd and ./.. on dir', () => {
    usePathCacheStore.getState().add('h1', '/repo/', 'sess1', '/repo/src/')
    usePathCacheStore.getState().add('h1', '/repo', 'sess1', '/repo/./src')
    usePathCacheStore.getState().add('h1', '/repo', 'sess1', '/repo/lib/../src')
    expect(dirsAt('h1', '/repo')).toEqual(['/repo/src'])
  })

  it('lookup returns same-session entries first then others by recency', () => {
    usePathCacheStore.getState().add('h1', '/repo', 'sessOLD', '/repo/old1')
    usePathCacheStore.getState().add('h1', '/repo', 'sessNEW', '/repo/new1')
    usePathCacheStore.getState().add('h1', '/repo', 'sessOLD', '/repo/old2')
    usePathCacheStore.getState().add('h1', '/repo', 'sessNEW', '/repo/new2')
    expect(usePathCacheStore.getState().lookup('h1', '/repo', 'foo.go', 'sessNEW')).toEqual([
      '/repo/new2/foo.go', '/repo/new1/foo.go', '/repo/old2/foo.go', '/repo/old1/foo.go',
    ])
  })

  it('lookup without currentSessionCode falls back to pure recency order', () => {
    usePathCacheStore.getState().add('h1', '/repo', 'sessA', '/repo/a')
    usePathCacheStore.getState().add('h1', '/repo', 'sessB', '/repo/b')
    expect(usePathCacheStore.getState().lookup('h1', '/repo', 'x.go')).toEqual([
      '/repo/b/x.go', '/repo/a/x.go',
    ])
  })

  it('lookup returns empty when scope has no entries', () => {
    expect(usePathCacheStore.getState().lookup('h1', '/repo', 'foo.go')).toEqual([])
  })

  it('pruneStaleCandidate removes the dirname entry; clears scope when last entry gone', () => {
    usePathCacheStore.getState().add('h1', '/repo', 'sess1', '/repo/src')
    usePathCacheStore.getState().pruneStaleCandidate('h1', '/repo', '/repo/src/foo.go')
    expect(usePathCacheStore.getState().entriesByScope[scopeKey('h1', '/repo')]).toBeUndefined()
  })

  it('clearScope removes only the named (host, cwd)', () => {
    usePathCacheStore.getState().add('h1', '/repo', 'sess1', '/repo/a')
    usePathCacheStore.getState().add('h1', '/other', 'sess1', '/other/b')
    usePathCacheStore.getState().add('h2', '/repo', 'sess1', '/repo/c')
    usePathCacheStore.getState().clearScope('h1', '/repo')
    expect(usePathCacheStore.getState().entriesByScope[scopeKey('h1', '/repo')]).toBeUndefined()
    expect(dirsAt('h1', '/other')).toEqual(['/other/b'])
    expect(dirsAt('h2', '/repo')).toEqual(['/repo/c'])
  })

  it('clearBySession purges entries for that session across all scopes; deletes empty scopes', () => {
    usePathCacheStore.getState().add('h1', '/repo', 'sessA', '/repo/a1')
    usePathCacheStore.getState().add('h1', '/repo', 'sessB', '/repo/b1')
    usePathCacheStore.getState().add('h1', '/other', 'sessA', '/other/a2')
    usePathCacheStore.getState().clearBySession('sessA')
    expect(dirsAt('h1', '/repo')).toEqual(['/repo/b1'])
    expect(usePathCacheStore.getState().entriesByScope[scopeKey('h1', '/other')]).toBeUndefined()
  })

  it('clearHost removes all scopes for that host (works with colon-containing hostId)', () => {
    usePathCacheStore.getState().add('mlab:abc', '/repo', 'sess1', '/repo/a')
    usePathCacheStore.getState().add('h2', '/repo', 'sess1', '/repo/b')
    usePathCacheStore.getState().clearHost('mlab:abc')
    expect(usePathCacheStore.getState().entriesByScope[scopeKey('mlab:abc', '/repo')]).toBeUndefined()
    expect(dirsAt('h2', '/repo')).toEqual(['/repo/b'])
  })
})

describe('sanitizeRehydratedPathCache', () => {
  it('keeps a well-formed entriesByScope intact', () => {
    const state = {
      entriesByScope: {
        [scopeKey('h1', '/repo')]: [{ dir: '/repo/src', sessionCode: 's1', touchedAt: 1 }],
      } as Record<string, unknown>,
    }
    sanitizeRehydratedPathCache(state, null)
    expect(state.entriesByScope).toEqual({
      [scopeKey('h1', '/repo')]: [{ dir: '/repo/src', sessionCode: 's1', touchedAt: 1 }],
    })
  })

  it('drops persisted entries that bypass invariants (relative dir, oversized, control char, dotdot)', () => {
    const state = {
      entriesByScope: {
        [scopeKey('h1', '/repo')]: [
          { dir: 'rel/path', sessionCode: 's', touchedAt: 1 },
          { dir: '/repo/' + 'x'.repeat(5000), sessionCode: 's', touchedAt: 2 },
          { dir: '/repo/\x00n', sessionCode: 's', touchedAt: 3 },
          { dir: '/repo/lib/../src', sessionCode: 's', touchedAt: 4 }, // normalizes to /repo/src
          { dir: '/repo/src', sessionCode: 's', touchedAt: 5 },         // dup of above after normalize → drop
        ],
      } as Record<string, unknown>,
    }
    sanitizeRehydratedPathCache(state, null)
    const list = (state.entriesByScope as Record<string, unknown[]>)[scopeKey('h1', '/repo')]
    expect(list).toEqual([{ dir: '/repo/src', sessionCode: 's', touchedAt: 4 }])
  })

  it('caps persisted entries at 50 per scope', () => {
    const big = Array.from({ length: 60 }, (_, i) => ({ dir: `/repo/d${i}`, sessionCode: 's', touchedAt: i }))
    const state = { entriesByScope: { [scopeKey('h1', '/repo')]: big } as Record<string, unknown> }
    sanitizeRehydratedPathCache(state, null)
    expect((state.entriesByScope as Record<string, unknown[]>)[scopeKey('h1', '/repo')]?.length).toBe(50)
  })

  it('drops malformed scope keys', () => {
    const state = {
      entriesByScope: {
        'no-separator-here': [{ dir: '/x', sessionCode: 's', touchedAt: 0 }],
        [scopeKey('h1', '/repo')]: [{ dir: '/repo/src', sessionCode: 's', touchedAt: 1 }],
      } as Record<string, unknown>,
    }
    sanitizeRehydratedPathCache(state, null)
    expect(Object.keys(state.entriesByScope as Record<string, unknown>)).toEqual([scopeKey('h1', '/repo')])
  })

  it('resets when rehydrate received an error', () => {
    const state = { entriesByScope: { [scopeKey('h1', '/repo')]: [{ dir: '/repo/src', sessionCode: 's', touchedAt: 1 }] } as Record<string, unknown> }
    sanitizeRehydratedPathCache(state, new Error('boom'))
    expect(state.entriesByScope).toEqual({})
  })

  it('resets when entriesByScope is missing / null / wrong type', () => {
    for (const bad of [{}, { entriesByScope: null }, { entriesByScope: 'string' }, { entriesByScope: [1, 2, 3] }]) {
      sanitizeRehydratedPathCache(bad as never, null)
      expect((bad as { entriesByScope: unknown }).entriesByScope).toEqual({})
    }
  })

  it('no-op when state is undefined', () => {
    expect(() => sanitizeRehydratedPathCache(undefined, null)).not.toThrow()
  })
})
