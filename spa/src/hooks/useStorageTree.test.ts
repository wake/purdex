import { describe, it, expect, beforeEach, vi } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { useStorageTree } from './useStorageTree'
import type { FsBackend } from '../lib/fs-backend'
import type { FileEntry } from '../types/fs'

// The hook resolves its backend via getFsBackend({type:'inapp'}). We swap a
// per-test fake into the registry mock (same pattern as EditorBuffersPane.test).
let mockBackend: FsBackend | undefined

vi.mock('../lib/fs-backend', () => ({
  getFsBackend: () => mockBackend,
  registerFsBackend: vi.fn(),
}))

function createBackend(paths: Map<string, { isDir: boolean; size: number }>): FsBackend {
  const list = vi.fn(async (path: string): Promise<FileEntry[]> => {
    const prefix = path.endsWith('/') ? path : path + '/'
    const seen = new Map<string, FileEntry>()
    for (const [p, meta] of paths) {
      if (!p.startsWith(prefix)) continue
      const rest = p.slice(prefix.length)
      if (rest.includes('/')) continue
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

function fixture(): Map<string, { isDir: boolean; size: number }> {
  return new Map([
    ['/buffer/a.md', { isDir: false, size: 5 }],
    ['/buffer/dir', { isDir: true, size: 0 }],
    ['/buffer/dir/b.md', { isDir: false, size: 7 }],
    ['/buffer/dir/sub', { isDir: true, size: 0 }],
    ['/buffer/dir/sub/c.txt', { isDir: false, size: 3 }],
  ])
}

describe('useStorageTree', () => {
  beforeEach(() => {
    mockBackend = createBackend(fixture())
  })

  it('builds the nested tree from the inapp backend', async () => {
    const { result } = renderHook(() => useStorageTree())
    expect(result.current.loading).toBe(true)
    await waitFor(() => expect(result.current.loading).toBe(false))

    expect(result.current.error).toBeNull()
    expect(result.current.tree.map((n) => n.name)).toEqual(['dir', 'a.md'])
    const dir = result.current.tree[0]
    expect(dir.children!.map((n) => n.name)).toEqual(['sub', 'b.md'])
  })

  it('toggles expand/collapse keyed by FULL path', async () => {
    const { result } = renderHook(() => useStorageTree())
    await waitFor(() => expect(result.current.loading).toBe(false))

    expect(result.current.expanded.has('/buffer/dir')).toBe(false)
    act(() => result.current.toggle('/buffer/dir'))
    expect(result.current.expanded.has('/buffer/dir')).toBe(true)

    act(() => result.current.toggle('/buffer/dir'))
    expect(result.current.expanded.has('/buffer/dir')).toBe(false)
  })

  it('distinguishes same-basename dirs by full path in expanded set', async () => {
    mockBackend = createBackend(
      new Map([
        ['/buffer/d1', { isDir: true, size: 0 }],
        ['/buffer/d1/x.md', { isDir: false, size: 1 }],
        ['/buffer/d2', { isDir: true, size: 0 }],
        ['/buffer/d2/x.md', { isDir: false, size: 1 }],
      ]),
    )
    const { result } = renderHook(() => useStorageTree())
    await waitFor(() => expect(result.current.loading).toBe(false))

    act(() => result.current.toggle('/buffer/d1'))
    expect(result.current.expanded.has('/buffer/d1')).toBe(true)
    expect(result.current.expanded.has('/buffer/d2')).toBe(false)
  })

  it('re-reads the backend on refresh', async () => {
    const { result } = renderHook(() => useStorageTree())
    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.tree).toHaveLength(2)

    // Mutate the backing store, then refresh: tree picks up the new file.
    mockBackend = createBackend(
      new Map([
        ['/buffer/a.md', { isDir: false, size: 5 }],
        ['/buffer/new.txt', { isDir: false, size: 1 }],
      ]),
    )
    act(() => result.current.refresh())
    await waitFor(() => expect(result.current.tree.map((n) => n.name)).toEqual(['a.md', 'new.txt']))
  })

  it('surfaces an error when the inapp backend is unavailable', async () => {
    mockBackend = undefined
    const { result } = renderHook(() => useStorageTree())
    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.error).toBeTruthy()
    expect(result.current.tree).toEqual([])
  })
})
