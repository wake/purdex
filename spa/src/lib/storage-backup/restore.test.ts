import { describe, it, expect, beforeEach, vi } from 'vitest'
import { restoreSnapshot } from './restore'
import { InAppBackend } from '../fs-backend-inapp'
import { closeAllIDB } from '../storage/idb'
import { sha256Hex } from '../crypto-hash'
import type { SnapshotDetail } from './backup-api'
import { BackupNotFoundError } from './backup-api'
import type { ManifestEntry } from './manifest'

const enc = (s: string) => new TextEncoder().encode(s)
const dec = (b: Uint8Array) => new TextDecoder().decode(b)

function deleteInappDB(): Promise<void> {
  return new Promise((resolve, reject) => {
    const req = indexedDB.deleteDatabase('pdx-inapp-fs')
    req.onsuccess = () => resolve()
    req.onerror = () => reject(req.error)
    req.onblocked = () => reject(new Error('deleteDatabase blocked'))
  })
}

let backend: InAppBackend

beforeEach(async () => {
  await closeAllIDB()
  await deleteInappDB()
  backend = new InAppBackend()
})

/** Build a SnapshotDetail manifest from {path -> bytes|null(dir)} and a blob map. */
async function makeSnapshot(
  files: Record<string, string | null>,
): Promise<{ detail: SnapshotDetail; blobs: Map<string, Uint8Array> }> {
  const manifest: ManifestEntry[] = []
  const blobs = new Map<string, Uint8Array>()
  for (const [path, content] of Object.entries(files)) {
    if (content === null) {
      manifest.push({ path, kind: 'dir', hash: '', size: 0, words: 0 })
    } else {
      const bytes = enc(content)
      const hash = await sha256Hex(bytes)
      manifest.push({ path, kind: 'file', hash, size: bytes.byteLength, words: 0 })
      blobs.set(hash, bytes)
    }
  }
  const detail: SnapshotDetail = {
    id: 99,
    storeId: 'inapp:buffer',
    device: 'c_abc',
    parentId: null,
    isFork: false,
    trigger: 'auto',
    createdAt: 0,
    manifest,
  }
  return { detail, blobs }
}

describe('restoreSnapshot', () => {
  it('returns blocked and touches nothing when the guard reports conflicts', async () => {
    await backend.write('/buffer/a.txt', enc('orig'))
    const preRestore = vi.fn()
    const getSnapshot = vi.fn()
    const getBlob = vi.fn()
    const replaceSpy = vi.spyOn(backend, 'replaceTree')

    const res = await restoreSnapshot({
      hostId: 'h1',
      snapshotId: 99,
      backend,
      findConflicts: () => [{ type: 'dirty', tabId: 't1', filePath: '/buffer/a.txt' }],
      preRestore,
      getSnapshot,
      getBlob,
    })

    expect(res).toEqual({
      status: 'blocked',
      conflicts: [{ type: 'dirty', tabId: 't1', filePath: '/buffer/a.txt' }],
    })
    expect(preRestore).not.toHaveBeenCalled()
    expect(getSnapshot).not.toHaveBeenCalled()
    expect(getBlob).not.toHaveBeenCalled()
    expect(replaceSpy).not.toHaveBeenCalled()
    expect(dec(await backend.read('/buffer/a.txt'))).toBe('orig')
  })

  it('aborts (throws) and touches nothing when the pre-restore snapshot fails (null)', async () => {
    // forcePost pre-restore returns null ONLY on failure (daemon down, build /
    // post error). With no restore-point, restore must NOT proceed to wipe the
    // tree — there would be nothing to roll back to (codex 2c-1 R1 P1).
    await backend.write('/buffer/a.txt', enc('orig'))
    const preRestore = vi.fn().mockResolvedValue(null)
    const getSnapshot = vi.fn()
    const getBlob = vi.fn()
    const replaceSpy = vi.spyOn(backend, 'replaceTree')

    await expect(
      restoreSnapshot({
        hostId: 'h1',
        snapshotId: 99,
        backend,
        findConflicts: () => [],
        preRestore,
        getSnapshot,
        getBlob,
      }),
    ).rejects.toThrow(/pre-restore/i)

    expect(preRestore).toHaveBeenCalledTimes(1)
    expect(getSnapshot).not.toHaveBeenCalled() // aborted before any fetch
    expect(getBlob).not.toHaveBeenCalled()
    expect(replaceSpy).not.toHaveBeenCalled() // tree never mutated
    expect(dec(await backend.read('/buffer/a.txt'))).toBe('orig')
  })

  it('aborts (no overwrite) when a concurrent /buffer write lands during restore', async () => {
    // A concurrent local write (e.g. another pane autosave) lands AFTER the
    // revision baseline + safety snapshot but BEFORE apply. It bumps the tree
    // revision, so replaceTree's in-txn revision guard aborts the wipe rather
    // than silently overwriting it (codex 2c-1 R3 Critical, atomic guard).
    await backend.write('/buffer/a.txt', enc('orig'))
    const { detail, blobs } = await makeSnapshot({ 'a.txt': 'restored' })

    await expect(
      restoreSnapshot({
        hostId: 'h1',
        snapshotId: 99,
        backend,
        findConflicts: () => [],
        preRestore: vi.fn().mockResolvedValue(50),
        getSnapshot: vi.fn(async () => {
          await backend.write('/buffer/new.txt', enc('concurrent')) // bumps revision
          return detail
        }),
        getBlob: vi.fn(async (_h: string, hash: string) => blobs.get(hash)!),
      }),
    ).rejects.toThrow(/tree changed during restore/i)

    // Both the original and the concurrent write survive — tree never wiped.
    expect(dec(await backend.read('/buffer/a.txt'))).toBe('orig')
    expect(dec(await backend.read('/buffer/new.txt'))).toBe('concurrent')
  })

  it('aborts before apply if a buffer becomes dirty/locked during restore', async () => {
    await backend.write('/buffer/a.txt', enc('orig'))
    const { detail, blobs } = await makeSnapshot({ 'a.txt': 'restored' })
    const replaceSpy = vi.spyOn(backend, 'replaceTree')
    const findConflicts = vi
      .fn()
      .mockReturnValueOnce([]) // clean at start
      .mockReturnValue([{ type: 'dirty', tabId: 't1', filePath: '/buffer/a.txt' }]) // dirty by apply

    await expect(
      restoreSnapshot({
        hostId: 'h1',
        snapshotId: 99,
        backend,
        findConflicts,
        preRestore: vi.fn().mockResolvedValue(50),
        getSnapshot: vi.fn().mockResolvedValue(detail),
        getBlob: vi.fn(async (_h: string, hash: string) => blobs.get(hash)!),
      }),
    ).rejects.toThrow(/dirty/i)

    expect(replaceSpy).not.toHaveBeenCalled()
    expect(dec(await backend.read('/buffer/a.txt'))).toBe('orig')
  })

  it('rejects a manifest with the same hash but a conflicting declared size (per-entry size check)', async () => {
    await backend.write('/buffer/x.txt', enc('orig'))
    const bytes = enc('shared')
    const hash = await sha256Hex(bytes)
    const detail: SnapshotDetail = {
      id: 99,
      storeId: 'inapp:buffer',
      device: 'c_abc',
      parentId: null,
      isFork: false,
      trigger: 'auto',
      createdAt: 0,
      manifest: [
        { path: 'a.txt', kind: 'file', hash, size: bytes.byteLength, words: 0 },
        { path: 'b.txt', kind: 'file', hash, size: bytes.byteLength + 7, words: 0 }, // same hash, wrong size
      ],
    }
    const replaceSpy = vi.spyOn(backend, 'replaceTree')

    await expect(
      restoreSnapshot({
        hostId: 'h1',
        snapshotId: 99,
        backend,
        findConflicts: () => [],
        preRestore: vi.fn().mockResolvedValue(50),
        getSnapshot: vi.fn().mockResolvedValue(detail),
        getBlob: vi.fn(async () => bytes),
      }),
    ).rejects.toThrow(/size mismatch/i)

    expect(replaceSpy).not.toHaveBeenCalled()
  })

  it('posts the pre-restore safety snapshot BEFORE fetching any blob', async () => {
    await backend.write('/buffer/a.md', enc('orig'))
    const { detail, blobs } = await makeSnapshot({ 'a.md': 'restored' })

    const order: string[] = []
    const preRestore = vi.fn(async () => {
      order.push('preRestore')
      return 50
    })
    const getSnapshot = vi.fn(async () => {
      order.push('getSnapshot')
      return detail
    })
    const getBlob = vi.fn(async (_h: string, hash: string) => {
      order.push('getBlob')
      return blobs.get(hash)!
    })

    await restoreSnapshot({
      hostId: 'h1',
      snapshotId: 99,
      backend,
      findConflicts: () => [],
      preRestore,
      getSnapshot,
      getBlob,
    })

    expect(order.indexOf('preRestore')).toBeLessThan(order.indexOf('getBlob'))
    expect(order.indexOf('preRestore')).toBeLessThan(order.indexOf('getSnapshot'))
  })

  it('records the pre-restore head id as the restore-point on a content-equal tree', async () => {
    // Current tree already equals the snapshot — pre-restore is a daemon no-op
    // returning the head id, which IS the restore-point.
    await backend.write('/buffer/a.md', enc('same'))
    const { detail, blobs } = await makeSnapshot({ 'a.md': 'same' })

    const preRestore = vi.fn(async () => 42) // daemon no-op → head id
    const res = await restoreSnapshot({
      hostId: 'h1',
      snapshotId: 99,
      backend,
      findConflicts: () => [],
      preRestore,
      getSnapshot: async () => detail,
      getBlob: async (_h, hash) => blobs.get(hash)!,
    })

    expect(res.status).toBe('done')
    if (res.status === 'done') {
      expect(res.restorePointId).toBe(42)
      expect(res.changed).toEqual({ added: [], removed: [], modified: [] })
    }
  })

  it('aborts before any IDB mutation when a blob fetch fails (atomic rollback)', async () => {
    await backend.write('/buffer/keep.txt', enc('keep'))
    const { detail, blobs } = await makeSnapshot({ 'a.md': 'A', 'b.md': 'B' })
    const goodHash = (await sha256Hex(enc('A'))) // a.md
    const replaceSpy = vi.spyOn(backend, 'replaceTree')

    const getBlob = vi.fn(async (_h: string, hash: string) => {
      if (hash === goodHash) return blobs.get(hash)!
      throw new BackupNotFoundError('blob not found: 404')
    })

    await expect(
      restoreSnapshot({
        hostId: 'h1',
        snapshotId: 99,
        backend,
        findConflicts: () => [],
        preRestore: async () => 10,
        getSnapshot: async () => detail,
        getBlob,
      }),
    ).rejects.toThrow()

    expect(replaceSpy).not.toHaveBeenCalled()
    expect(dec(await backend.read('/buffer/keep.txt'))).toBe('keep')
    await expect(backend.read('/buffer/a.md')).rejects.toThrow()
  })

  it('throws (no replaceTree) when a fetched blob fails sha256/size verification', async () => {
    await backend.write('/buffer/keep.txt', enc('keep'))
    const { detail } = await makeSnapshot({ 'a.md': 'A' })
    const replaceSpy = vi.spyOn(backend, 'replaceTree')

    await expect(
      restoreSnapshot({
        hostId: 'h1',
        snapshotId: 99,
        backend,
        findConflicts: () => [],
        preRestore: async () => 10,
        getSnapshot: async () => detail,
        // wrong bytes for the declared hash → verification must fail
        getBlob: async () => enc('TAMPERED'),
      }),
    ).rejects.toThrow()

    expect(replaceSpy).not.toHaveBeenCalled()
    expect(dec(await backend.read('/buffer/keep.txt'))).toBe('keep')
  })

  it('happy path: replaceTree applies the exact manifest and changed diff is correct', async () => {
    await backend.write('/buffer/old.txt', enc('old'))
    await backend.write('/buffer/keep.md', enc('v1'))

    const { detail, blobs } = await makeSnapshot({
      docs: null, // empty dir
      'docs/new.md': 'new',
      'keep.md': 'v2', // modified
      // old.txt removed
    })

    const res = await restoreSnapshot({
      hostId: 'h1',
      snapshotId: 99,
      backend,
      findConflicts: () => [],
      preRestore: async () => 100,
      getSnapshot: async () => detail,
      getBlob: async (_h, hash) => blobs.get(hash)!,
    })

    expect(res.status).toBe('done')
    if (res.status === 'done') {
      expect(res.restorePointId).toBe(100)
      expect({
        added: [...res.changed.added].sort(),
        removed: [...res.changed.removed].sort(),
        modified: [...res.changed.modified].sort(),
      }).toEqual({
        added: ['docs', 'docs/new.md'],
        removed: ['old.txt'],
        modified: ['keep.md'],
      })
    }

    // The tree now matches the manifest exactly.
    expect(dec(await backend.read('/buffer/keep.md'))).toBe('v2')
    expect(dec(await backend.read('/buffer/docs/new.md'))).toBe('new')
    expect((await backend.stat('/buffer/docs')).isDirectory).toBe(true)
    await expect(backend.read('/buffer/old.txt')).rejects.toThrow()
  })
})
