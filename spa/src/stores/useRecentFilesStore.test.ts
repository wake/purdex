import { describe, it, expect, beforeEach } from 'vitest'
import { useRecentFilesStore, recentKey, type RecentFileEntry } from './useRecentFilesStore'
import type { FileSource } from '../types/fs'

const entry = (over: Partial<RecentFileEntry> = {}): RecentFileEntry => ({
  source: { type: 'inapp' },
  path: '/buffer/a.md',
  name: 'a.md',
  kind: 'editor',
  openedAt: 1,
  ...over,
})

describe('useRecentFilesStore', () => {
  beforeEach(() => useRecentFilesStore.setState({ files: [] }))

  it('adds to front', () => {
    useRecentFilesStore.getState().addRecent(entry({ path: '/x.md', name: 'x.md' }))
    useRecentFilesStore.getState().addRecent(entry({ path: '/y.md', name: 'y.md' }))
    expect(useRecentFilesStore.getState().files.map((f) => f.path)).toEqual(['/y.md', '/x.md'])
  })

  it('dedups by source+path, moving to front', () => {
    useRecentFilesStore.getState().addRecent(entry({ path: '/x.md' }))
    useRecentFilesStore.getState().addRecent(entry({ path: '/y.md' }))
    useRecentFilesStore.getState().addRecent(entry({ path: '/x.md', openedAt: 9 }))
    const files = useRecentFilesStore.getState().files
    expect(files.map((f) => f.path)).toEqual(['/x.md', '/y.md'])
    expect(files[0].openedAt).toBe(9)
  })

  it('treats same path on different hosts as distinct', () => {
    const d1: FileSource = { type: 'daemon', hostId: 'h1' }
    const d2: FileSource = { type: 'daemon', hostId: 'h2' }
    useRecentFilesStore.getState().addRecent(entry({ source: d1, path: '/p' }))
    useRecentFilesStore.getState().addRecent(entry({ source: d2, path: '/p' }))
    expect(useRecentFilesStore.getState().files).toHaveLength(2)
  })

  it('caps at 50, dropping oldest', () => {
    for (let i = 0; i < 55; i++) {
      useRecentFilesStore.getState().addRecent(entry({ path: `/f${i}.md` }))
    }
    const files = useRecentFilesStore.getState().files
    expect(files).toHaveLength(50)
    expect(files[0].path).toBe('/f54.md')
    expect(files.at(-1)?.path).toBe('/f5.md')
  })

  it('recentKey distinguishes source type + host', () => {
    expect(recentKey({ type: 'inapp' }, '/p')).not.toBe(recentKey({ type: 'local' }, '/p'))
    expect(recentKey({ type: 'daemon', hostId: 'h1' }, '/p'))
      .not.toBe(recentKey({ type: 'daemon', hostId: 'h2' }, '/p'))
  })

  it('clear empties the list', () => {
    useRecentFilesStore.getState().addRecent(entry())
    useRecentFilesStore.getState().clear()
    expect(useRecentFilesStore.getState().files).toEqual([])
  })
})

describe('useRecentFilesStore — renamePath / removePath', () => {
  const inapp: FileSource = { type: 'inapp' }
  const seed = (entries: RecentFileEntry[]) => useRecentFilesStore.setState({ files: entries })
  const paths = () => useRecentFilesStore.getState().files.map((f) => f.path)

  beforeEach(() => useRecentFilesStore.setState({ files: [] }))

  it('renamePath on an exact match updates path and name, keeping kind and openedAt', () => {
    seed([entry({ path: '/buffer/a.md', name: 'a.md', kind: 'editor', openedAt: 7 })])
    useRecentFilesStore.getState().renamePath(inapp, '/buffer/a.md', '/buffer/sub/b.txt')
    const files = useRecentFilesStore.getState().files
    expect(files).toHaveLength(1)
    expect(files[0]).toMatchObject({
      path: '/buffer/sub/b.txt',
      name: 'b.txt',
      kind: 'editor',
      openedAt: 7,
    })
  })

  it('remaps every descendant of a folder rename without matching sibling prefixes', () => {
    seed([
      entry({ path: '/buffer/a', name: 'a' }),
      entry({ path: '/buffer/a/x.md', name: 'x.md' }),
      entry({ path: '/buffer/a/deep/y.md', name: 'y.md' }),
      entry({ path: '/buffer/ab', name: 'ab' }),
      entry({ path: '/buffer/ab/z.md', name: 'z.md' }),
    ])
    useRecentFilesStore.getState().renamePath(inapp, '/buffer/a', '/buffer/c')
    expect(paths()).toEqual([
      '/buffer/c',
      '/buffer/c/x.md',
      '/buffer/c/deep/y.md',
      '/buffer/ab',
      '/buffer/ab/z.md',
    ])
    expect(useRecentFilesStore.getState().files[0].name).toBe('c')
    expect(useRecentFilesStore.getState().files[2].name).toBe('y.md')
  })

  it('leaves other source types and other daemon hosts untouched', () => {
    const h1: FileSource = { type: 'daemon', hostId: 'h1' }
    const h2: FileSource = { type: 'daemon', hostId: 'h2' }
    seed([
      entry({ source: h1, path: '/p/a.md', name: 'a.md' }),
      entry({ source: h2, path: '/p/a.md', name: 'a.md' }),
      entry({ source: { type: 'local' }, path: '/p/a.md', name: 'a.md' }),
      entry({ source: inapp, path: '/p/a.md', name: 'a.md' }),
    ])
    useRecentFilesStore.getState().renamePath(h1, '/p/a.md', '/p/b.md')
    const files = useRecentFilesStore.getState().files
    expect(files.map((f) => f.path)).toEqual(['/p/b.md', '/p/a.md', '/p/a.md', '/p/a.md'])
    expect(files[1].source).toEqual(h2)
  })

  it('merges into a single destination entry keeping the newer openedAt', () => {
    seed([
      entry({ path: '/buffer/new.md', name: 'new.md', kind: 'editor', openedAt: 9 }),
      entry({ path: '/buffer/old.md', name: 'old.md', kind: 'image-preview', openedAt: 3 }),
    ])
    useRecentFilesStore.getState().renamePath(inapp, '/buffer/old.md', '/buffer/new.md')
    let files = useRecentFilesStore.getState().files
    expect(files).toHaveLength(1)
    expect(files[0]).toMatchObject({ path: '/buffer/new.md', name: 'new.md', openedAt: 9 })
    // The file now living at the destination is the renamed one, so its kind wins.
    expect(files[0].kind).toBe('image-preview')

    // Reverse recency: the renamed entry is the newer of the two.
    seed([
      entry({ path: '/buffer/new.md', name: 'new.md', openedAt: 3 }),
      entry({ path: '/buffer/old.md', name: 'old.md', openedAt: 9 }),
    ])
    useRecentFilesStore.getState().renamePath(inapp, '/buffer/old.md', '/buffer/new.md')
    files = useRecentFilesStore.getState().files
    expect(files).toHaveLength(1)
    expect(files[0]).toMatchObject({ path: '/buffer/new.md', openedAt: 9 })
  })

  it('removePath drops the entry and its descendants, sparing unrelated ones', () => {
    seed([
      entry({ path: '/buffer/a', name: 'a' }),
      entry({ path: '/buffer/a/x.md', name: 'x.md' }),
      entry({ path: '/buffer/ab/z.md', name: 'z.md' }),
      entry({ source: { type: 'daemon', hostId: 'h1' }, path: '/buffer/a/x.md', name: 'x.md' }),
    ])
    useRecentFilesStore.getState().removePath(inapp, '/buffer/a')
    const files = useRecentFilesStore.getState().files
    expect(files.map((f) => f.path)).toEqual(['/buffer/ab/z.md', '/buffer/a/x.md'])
    expect(files[1].source).toEqual({ type: 'daemon', hostId: 'h1' })
  })

  it('is a no-op when nothing matches', () => {
    seed([
      entry({ path: '/buffer/a.md', name: 'a.md', openedAt: 1 }),
      entry({ path: '/buffer/b.md', name: 'b.md', openedAt: 2 }),
    ])
    const before = useRecentFilesStore.getState().files
    useRecentFilesStore.getState().renamePath(inapp, '/buffer/zz.md', '/buffer/yy.md')
    expect(useRecentFilesStore.getState().files).toEqual(before)
    useRecentFilesStore.getState().removePath(inapp, '/buffer/zz.md')
    expect(useRecentFilesStore.getState().files).toEqual(before)
    useRecentFilesStore.getState().removePath({ type: 'local' }, '/buffer/a.md')
    expect(useRecentFilesStore.getState().files).toEqual(before)
  })
})
