import { describe, expect, it, beforeEach } from 'vitest'
import { createSnapshotStore } from '../snapshot-store'
import { closeAllIDB, openIDB } from '../../storage/idb'
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
    expect('bundle' in list[0]).toBe(false)
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

describe('SnapshotStore.rotateSessionPristine (Adv-1 atomicity)', () => {
  beforeEach(async () => {
    await closeAllIDB()
    indexedDB.deleteDatabase('purdex-sync-test')
  })

  it('creates a new pristine and demotes all prior pristine rows in one pass', async () => {
    const store = createSnapshotStore('purdex-sync-test')
    await store.init()

    // Seed two prior pristine snapshots
    await store.createSnapshot(bundle('old-a'), 'pre-restore', { isSessionPristine: true })
    await new Promise((r) => setTimeout(r, 2))
    await store.createSnapshot(bundle('old-b'), 'pre-restore', { isSessionPristine: true })

    const meta = await store.rotateSessionPristine(bundle('new'), 'pre-restore')
    expect(meta.isSessionPristine).toBe(true)

    const list = await store.listLocal()
    const pristineIds = list.filter((m) => m.isSessionPristine).map((m) => m.id)
    expect(pristineIds).toEqual([meta.id])
  })

  it('concurrent rotateSessionPristine calls end with exactly one pristine (no race)', async () => {
    const store = createSnapshotStore('purdex-sync-test')
    await store.init()

    // Fire two rotations without awaiting — each must land, and only one
    // row can keep isSessionPristine at the end.
    const [metaA, metaB] = await Promise.all([
      store.rotateSessionPristine(bundle('tab-a'), 'pre-restore'),
      store.rotateSessionPristine(bundle('tab-b'), 'pre-restore'),
    ])
    expect(metaA.id).not.toBe(metaB.id)

    const list = await store.listLocal()
    const pristine = list.filter((m) => m.isSessionPristine)
    expect(pristine).toHaveLength(1)
    // Whichever rotation committed last is the winner.
    expect([metaA.id, metaB.id]).toContain(pristine[0].id)
  })
})

// --- C5 (T4-4): the quota-retry fallback still works through the LIFTED
// `isQuotaError` (now imported from lib/quota). A first put that throws a quota
// DOMException must trigger compaction + a successful retry — proving the lift
// did not break detection.
describe('SnapshotStore.createSnapshot — quota-retry via the lifted isQuotaError (C5)', () => {
  beforeEach(async () => {
    await closeAllIDB()
    indexedDB.deleteDatabase('purdex-sync-test')
  })

  it('a quota-failed first put triggers compaction then a successful retry', async () => {
    const store = createSnapshotStore('purdex-sync-test')
    await store.init()

    // openIDB caches by (name, version), so this resolves to the SAME connection
    // the store uses → overriding `db.put` here intercepts the store's writes.
    const db = await openIDB('purdex-sync-test', 1, () => {})
    const realPut = db.put.bind(db)
    let putCalls = 0
    // Override the idb shorthand `put`: reject the FIRST call with a quota
    // DOMException (as a full IDB would), pass every later call through.
    const dbWithPut = db as unknown as { put: typeof db.put }
    dbWithPut.put = ((storeName: string, value: unknown) => {
      putCalls += 1
      if (putCalls === 1) {
        return Promise.reject(new DOMException('quota exceeded', 'QuotaExceededError'))
      }
      return realPut(storeName as never, value as never)
    }) as typeof db.put

    try {
      const meta = await store.createSnapshot(bundle(), 'manual')
      // First put rejected (quota) → compactFn() → retry put succeeded.
      expect(putCalls).toBeGreaterThanOrEqual(2)
      // The snapshot persisted despite the initial quota failure.
      const fetched = await store.getLocal(meta.id)
      expect(fetched).not.toBeNull()
    } finally {
      delete (db as unknown as { put?: unknown }).put
    }
  })

  it('a NON-quota put error is NOT retried — it propagates', async () => {
    const store = createSnapshotStore('purdex-sync-test')
    await store.init()
    const db = await openIDB('purdex-sync-test', 1, () => {})
    let putCalls = 0
    const dbWithPut = db as unknown as { put: typeof db.put }
    dbWithPut.put = (() => {
      putCalls += 1
      return Promise.reject(new Error('disk on fire'))
    }) as typeof db.put

    try {
      await expect(store.createSnapshot(bundle(), 'manual')).rejects.toThrow('disk on fire')
      // No compaction-retry for a non-quota error: put attempted exactly once.
      expect(putCalls).toBe(1)
    } finally {
      delete (db as unknown as { put?: unknown }).put
    }
  })
})
