import { describe, expect, it, beforeEach } from 'vitest'
import { createSnapshotStore } from '../snapshot-store'
import { closeAllIDB } from '../../storage/idb'
import type { SyncBundle } from '../types'

const bundle = (device = 'dev-a'): SyncBundle => ({
  version: 1,
  timestamp: Date.now(),
  device,
  collections: {
    workspaces: { version: 1, data: { list: [{ id: 'w1' }] } },
    hosts: { version: 1, data: { list: [] } },
  },
})

describe('SnapshotStore.createSnapshot + getLocal', () => {
  beforeEach(async () => {
    await closeAllIDB()
    indexedDB.deleteDatabase('purdex-sync-test')
  })

  it('stores a snapshot and retrieves it by id', async () => {
    const store = createSnapshotStore('purdex-sync-test')
    await store.init()

    const meta = await store.createSnapshot(bundle(), 'manual')

    expect(meta.id).toMatch(/^snap_/)
    expect(meta.trigger).toBe('manual')
    expect(meta.device).toBe('dev-a')
    expect(meta.contributorIds).toEqual(['workspaces', 'hosts'])
    expect(meta.bundleSize).toBeGreaterThan(0)
    expect(meta.isSessionPristine).toBe(false)

    const fetched = await store.getLocal(meta.id)
    expect(fetched).not.toBeNull()
    expect(fetched!.bundle.collections.workspaces).toBeDefined()
  })

  it('marks session-pristine when opts set', async () => {
    const store = createSnapshotStore('purdex-sync-test')
    await store.init()
    const meta = await store.createSnapshot(bundle(), 'pre-restore', { isSessionPristine: true })
    expect(meta.isSessionPristine).toBe(true)
  })
})
