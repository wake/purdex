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

    // Use pre-import trigger so auto-compact keeps both (pre-op pool max=5)
    const a = await store.createSnapshot(bundle('a'), 'pre-import')
    await new Promise((r) => setTimeout(r, 5))
    const b = await store.createSnapshot(bundle('b'), 'pre-import')

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

describe('SnapshotStore.compact + auto-compact', () => {
  beforeEach(async () => {
    await closeAllIDB()
    indexedDB.deleteDatabase('purdex-sync-test')
  })

  it('compact policy evicts oldest pre-op beyond max=5', async () => {
    const store = createSnapshotStore('purdex-sync-test')
    await store.init()

    const ids: string[] = []
    for (let i = 0; i < 6; i++) {
      const m = await store.createSnapshot(bundle(), 'pre-import')
      ids.push(m.id)
      await new Promise((r) => setTimeout(r, 2))
    }

    // After 6 creates, auto-compact has run; expect 5 remain and ids[0] is gone
    const list = await store.listLocal()
    expect(list).toHaveLength(5)
    expect(list.some((m) => m.id === ids[0])).toBe(false)
  })

  it('createSnapshot 後自動 compact', async () => {
    const store = createSnapshotStore('purdex-sync-test')
    await store.init()
    for (let i = 0; i < 7; i++) {
      await store.createSnapshot(bundle(), 'pre-import')
      await new Promise((r) => setTimeout(r, 2))
    }
    const list = await store.listLocal()
    expect(list.length).toBeLessThanOrEqual(5)
  })
})
