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

describe('SnapshotStore.listLocal', () => {
  beforeEach(async () => {
    await closeAllIDB()
    indexedDB.deleteDatabase('purdex-sync-test')
  })

  it('returns metadata only (no bundle) sorted newest first', async () => {
    const store = createSnapshotStore('purdex-sync-test')
    await store.init()

    const a = await store.createSnapshot(bundle('a'), 'manual')
    await new Promise((r) => setTimeout(r, 5))
    const b = await store.createSnapshot(bundle('b'), 'manual')

    const list = await store.listLocal()
    expect(list).toHaveLength(2)
    expect(list[0].id).toBe(b.id)
    expect(list[1].id).toBe(a.id)
    // bundle 不應該在 metadata 裡
    expect((list[0] as Record<string, unknown>).bundle).toBeUndefined()
  })

  it('deleteLocal removes one', async () => {
    const store = createSnapshotStore('purdex-sync-test')
    await store.init()
    const m = await store.createSnapshot(bundle(), 'manual')
    await store.deleteLocal(m.id)
    expect(await store.getLocal(m.id)).toBeNull()
  })

  it('clear empties store', async () => {
    const store = createSnapshotStore('purdex-sync-test')
    await store.init()
    await store.createSnapshot(bundle(), 'manual')
    await store.createSnapshot(bundle(), 'manual')
    await store.clear()
    expect(await store.listLocal()).toHaveLength(0)
  })
})
