import { describe, it, expect, vi, beforeEach } from 'vitest'
import { InAppBackend } from '../lib/fs-backend-inapp'
import { closeAllIDB } from '../lib/storage/idb'
import { registerFsBackend, clearFsBackendRegistry } from '../lib/fs-backend'
import { sha256Hex } from '../lib/crypto-hash'
import { useSyncStore } from '../lib/sync/use-sync-store'
import { useBackupStore } from './useBackupStore'

const enc = (s: string) => new TextEncoder().encode(s)

// Mocked daemon API client — the engine's negotiation/upload/post seam.
const postMissing = vi.fn()
const putBlob = vi.fn()
const postSnapshot = vi.fn()
vi.mock('../lib/storage-backup/backup-api', () => ({
  postMissing: (...a: unknown[]) => postMissing(...a),
  putBlob: (...a: unknown[]) => putBlob(...a),
  postSnapshot: (...a: unknown[]) => postSnapshot(...a),
}))

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
  clearFsBackendRegistry()
  backend = new InAppBackend()
  registerFsBackend('inapp', backend)
  useBackupStore.setState({ byHost: {} })
  postMissing.mockReset()
  putBlob.mockReset()
  postSnapshot.mockReset()
  // Defaults: daemon lacks every negotiated hash; snapshot writes id 1.
  postMissing.mockImplementation((_h: string, hashes: string[]) => Promise.resolve(hashes))
  putBlob.mockResolvedValue(undefined)
  postSnapshot.mockResolvedValue({ snapshotId: 1, isFork: false, currentHeadId: 1 })
})

const state = (host: string) => useBackupStore.getState().byHost[host]

describe('useBackupStore — backup engine', () => {
  it('first backup: builds manifest, uploads all missing blobs, posts, advances lineage', async () => {
    await backend.write('/buffer/a.txt', enc('one two'))
    await backend.write('/buffer/b.txt', enc('three'))
    postSnapshot.mockResolvedValue({ snapshotId: 7, isFork: false, currentHeadId: 7 })

    await useBackupStore.getState().backupNow('h1')

    expect(putBlob).toHaveBeenCalledTimes(2) // two distinct blobs
    expect(postSnapshot).toHaveBeenCalledTimes(1)
    const s = state('h1')
    expect(s.status).toBe('idle')
    expect(s.lastSnapshotId).toBe(7)
    expect(s.lastManifestJSON).toBeTruthy()
    expect(s.lastBackupAt).toBeGreaterThan(0)
    expect(s.lastError).toBeNull()
  })

  it('sends device = getClientId() and parentId = null on the first backup', async () => {
    await backend.write('/buffer/a.txt', enc('hi'))
    await useBackupStore.getState().backupNow('h1')
    const req = postSnapshot.mock.calls[0][1]
    expect(req.device).toBe(useSyncStore.getState().getClientId())
    expect(req.parentId).toBeNull()
    expect(req.storeId).toBe('inapp:buffer')
    expect(req.trigger).toBe('auto')
  })

  it('no-op: an unchanged tree skips negotiation + post entirely', async () => {
    await backend.write('/buffer/a.txt', enc('hi'))
    await useBackupStore.getState().backupNow('h1')
    postMissing.mockClear()
    postSnapshot.mockClear()

    await useBackupStore.getState().backupNow('h1') // tree unchanged
    expect(postMissing).not.toHaveBeenCalled()
    expect(postSnapshot).not.toHaveBeenCalled()
  })

  it('stale-lineage server no-op converges lineage so the next backup is suppressed (R2-P1a)', async () => {
    await backend.write('/buffer/a.txt', enc('hi'))
    // Daemon already has the blob and the manifest equals head: no-op post
    // returns the head id with no new row.
    postMissing.mockResolvedValue([])
    postSnapshot.mockResolvedValue({ snapshotId: 42, isFork: false, currentHeadId: 42 })

    await useBackupStore.getState().backupNow('h1') // local lineage was null
    expect(postSnapshot).toHaveBeenCalledTimes(1)
    expect(state('h1').lastSnapshotId).toBe(42) // converged to head

    await useBackupStore.getState().backupNow('h1') // immediate retry
    expect(postSnapshot).toHaveBeenCalledTimes(1) // suppressed client-side, no repeat no-op
  })

  it('dedup: only the changed file’s new blob is uploaded on the next backup', async () => {
    await backend.write('/buffer/f1.txt', enc('aaa'))
    await backend.write('/buffer/f2.txt', enc('bbb'))
    await useBackupStore.getState().backupNow('h1')
    putBlob.mockClear()

    await backend.write('/buffer/f2.txt', enc('ccc')) // change f2 only
    const newHash = await sha256Hex(enc('ccc'))
    postMissing.mockResolvedValueOnce([newHash]) // daemon lacks only the new blob

    await useBackupStore.getState().backupNow('h1')
    expect(putBlob).toHaveBeenCalledTimes(1)
    expect(putBlob.mock.calls[0][1]).toBe(newHash)
    expect(postSnapshot).toHaveBeenCalledTimes(2)
  })

  it('same-bytes rename: missing=[] → 0 PUT, 1 POST (manifest differs by path)', async () => {
    await backend.write('/buffer/a.txt', enc('hello'))
    await useBackupStore.getState().backupNow('h1')
    putBlob.mockClear()
    postSnapshot.mockClear()

    await backend.rename('/buffer/a.txt', '/buffer/b.txt')
    postMissing.mockResolvedValueOnce([]) // daemon already has the (unchanged) blob

    await useBackupStore.getState().backupNow('h1')
    expect(putBlob).not.toHaveBeenCalled()
    expect(postSnapshot).toHaveBeenCalledTimes(1)
  })

  it('intra-snapshot duplicate content uploads the shared blob once (R1-P3a)', async () => {
    await backend.write('/buffer/x.txt', enc('same'))
    await backend.write('/buffer/y.txt', enc('same'))
    await useBackupStore.getState().backupNow('h1')
    expect(putBlob).toHaveBeenCalledTimes(1)
  })

  it('parentId on the next backup is the prior returned snapshotId, never currentHeadId', async () => {
    await backend.write('/buffer/a.txt', enc('v1'))
    postSnapshot.mockResolvedValueOnce({ snapshotId: 5, isFork: false, currentHeadId: 99 })
    await useBackupStore.getState().backupNow('h1')

    await backend.write('/buffer/a.txt', enc('v2'))
    postSnapshot.mockResolvedValueOnce({ snapshotId: 6, isFork: false, currentHeadId: 99 })
    await useBackupStore.getState().backupNow('h1')

    expect(postSnapshot.mock.calls[1][1].parentId).toBe(5) // own prior id, not 99
  })

  it('applyRemoteBackupDone updates status/time only, never the own lineage (R1-P1a)', async () => {
    await backend.write('/buffer/a.txt', enc('v1'))
    postSnapshot.mockResolvedValueOnce({ snapshotId: 5, isFork: false, currentHeadId: 5 })
    await useBackupStore.getState().backupNow('h1')
    const manifestBefore = state('h1').lastManifestJSON

    useBackupStore.getState().applyRemoteBackupDone('h1', {
      storeId: 'inapp:buffer', snapshotId: 99, currentHeadId: 99,
      device: 'other', trigger: 'auto', createdAt: 1_751_230_000,
    })
    expect(state('h1').lastSnapshotId).toBe(5) // unchanged
    expect(state('h1').lastManifestJSON).toBe(manifestBefore) // unchanged
    expect(state('h1').lastBackupAt).toBe(1_751_230_000 * 1000)

    // The next backup still posts with the OWN prior parentId (5), not 99.
    await backend.write('/buffer/a.txt', enc('v2'))
    postSnapshot.mockResolvedValueOnce({ snapshotId: 6, isFork: false, currentHeadId: 99 })
    await useBackupStore.getState().backupNow('h1')
    expect(postSnapshot.mock.calls[1][1].parentId).toBe(5)
  })

  it('a failed upload sets status:error + lastError without advancing lineage', async () => {
    await backend.write('/buffer/a.txt', enc('hi'))
    putBlob.mockRejectedValueOnce(new Error('backup/blob PUT failed: 413'))

    await useBackupStore.getState().backupNow('h1')
    const s = state('h1')
    expect(s.status).toBe('error')
    expect(s.lastError).toMatch(/413/)
    expect(s.lastSnapshotId).toBeNull()
    expect(s.lastManifestJSON).toBeNull()
    expect(postSnapshot).not.toHaveBeenCalled()
  })

  it('keeps per-host state independent', async () => {
    await backend.write('/buffer/a.txt', enc('hi'))
    postSnapshot.mockResolvedValue({ snapshotId: 3, isFork: false, currentHeadId: 3 })
    await useBackupStore.getState().backupNow('h2')
    expect(state('h2').lastSnapshotId).toBe(3)
    expect(state('h1')).toBeUndefined() // h1 untouched
  })
})
