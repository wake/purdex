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
