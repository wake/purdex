import { describe, it, expect, vi } from 'vitest'
import { listTreeUnder, type TreeNode } from './storage-tree'
import type { FsBackend } from './fs-backend'
import type { FileEntry } from '../types/fs'

// Build a fake FsBackend whose `list(prefix)` mirrors InAppBackend.list:
// returns only direct children of `prefix` (implicit dir entries included).
// Returns entries in INSERTION order (deliberately unsorted) so the tests
// verify listTreeUnder applies its own dirs-first/name sort.
function createBackend(paths: Map<string, { isDir: boolean; size: number }>): FsBackend {
  const list = vi.fn(async (path: string): Promise<FileEntry[]> => {
    const prefix = path.endsWith('/') ? path : path + '/'
    const seen = new Map<string, FileEntry>()
    for (const [p, meta] of paths) {
      if (!p.startsWith(prefix)) continue
      const rest = p.slice(prefix.length)
      if (rest.includes('/')) continue // direct children only
      if (!seen.has(rest)) seen.set(rest, { name: rest, isDir: meta.isDir, size: meta.size })
    }
    return Array.from(seen.values())
  })
  return {
    id: 'inapp',
    label: 'In-App Storage',
    available: () => true,
    read: vi.fn(),
    write: vi.fn(),
    stat: vi.fn(),
    mkdir: vi.fn(),
    delete: vi.fn(),
    rename: vi.fn(),
    list,
  } as unknown as FsBackend
}

// Fixture: a.md at root; nested dir/b.md and dir/sub/c.txt. Dir entries are
// materialized (as InAppBackend.write auto-creates them). Insertion order is
// intentionally file-before-dir so sorting is observable.
function fixture(): Map<string, { isDir: boolean; size: number }> {
  return new Map([
    ['/buffer/a.md', { isDir: false, size: 5 }],
    ['/buffer/dir', { isDir: true, size: 0 }],
    ['/buffer/dir/b.md', { isDir: false, size: 7 }],
    ['/buffer/dir/sub', { isDir: true, size: 0 }],
    ['/buffer/dir/sub/c.txt', { isDir: false, size: 3 }],
  ])
}

describe('listTreeUnder', () => {
  it('enumerates the full nested structure under the root', async () => {
    const backend = createBackend(fixture())
    const tree = await listTreeUnder(backend, '/buffer')

    // Top level: dir (folder) sorted before a.md (file).
    expect(tree.map((n) => n.name)).toEqual(['dir', 'a.md'])

    const dir = tree[0]
    expect(dir.isDir).toBe(true)
    expect(dir.path).toBe('/buffer/dir')
    expect(dir.children).toBeDefined()
    // Inside dir: sub (folder) before b.md (file).
    expect(dir.children!.map((n) => n.name)).toEqual(['sub', 'b.md'])

    const sub = dir.children![0]
    expect(sub.path).toBe('/buffer/dir/sub')
    expect(sub.children!.map((n) => n.name)).toEqual(['c.txt'])

    const c = sub.children![0]
    expect(c.path).toBe('/buffer/dir/sub/c.txt')
    expect(c.isDir).toBe(false)
    expect(c.size).toBe(3)
    expect(c.children).toBeUndefined()
  })

  it('sorts dirs-first then by name at every level', async () => {
    const backend = createBackend(
      new Map([
        ['/buffer/z.md', { isDir: false, size: 1 }],
        ['/buffer/a.md', { isDir: false, size: 1 }],
        ['/buffer/m', { isDir: true, size: 0 }],
        ['/buffer/m/x', { isDir: false, size: 1 }],
        ['/buffer/b', { isDir: true, size: 0 }],
      ]),
    )
    const tree = await listTreeUnder(backend, '/buffer')
    expect(tree.map((n) => n.name)).toEqual(['b', 'm', 'a.md', 'z.md'])
  })

  it('gives two same-basename files in different dirs distinct full-path identities', async () => {
    const backend = createBackend(
      new Map([
        ['/buffer/d1', { isDir: true, size: 0 }],
        ['/buffer/d1/x.md', { isDir: false, size: 1 }],
        ['/buffer/d2', { isDir: true, size: 0 }],
        ['/buffer/d2/x.md', { isDir: false, size: 1 }],
      ]),
    )
    const tree = await listTreeUnder(backend, '/buffer')
    const [d1, d2] = tree
    const x1 = d1.children![0]
    const x2 = d2.children![0]
    expect(x1.name).toBe('x.md')
    expect(x2.name).toBe('x.md')
    expect(x1.path).toBe('/buffer/d1/x.md')
    expect(x2.path).toBe('/buffer/d2/x.md')
    expect(x1.path).not.toBe(x2.path)
  })

  it('returns an empty array for an empty store', async () => {
    const backend = createBackend(new Map())
    const tree = await listTreeUnder(backend, '/buffer')
    expect(tree).toEqual([])
  })

  it('represents an empty directory with an empty children array', async () => {
    const backend = createBackend(new Map([['/buffer/empty', { isDir: true, size: 0 }]]))
    const tree = await listTreeUnder(backend, '/buffer')
    const empty: TreeNode = tree[0]
    expect(empty.isDir).toBe(true)
    expect(empty.children).toEqual([])
  })
})
