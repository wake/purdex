import { openIDB } from '../storage/idb'
import { isQuotaError } from '../quota'
import type { SyncBundle } from './types'
import type { SnapshotMetadata, SnapshotTrigger, StoredSnapshot } from './snapshot-types'
import { computeCompaction } from './snapshot-compaction'

const STORE = 'snapshots'
const DB_VERSION = 1

function genId(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(8))
  const hex = Array.from(bytes).map((b) => b.toString(16).padStart(2, '0')).join('')
  return `snap_${hex}`
}

function computeBundleSize(bundle: SyncBundle): number {
  return new TextEncoder().encode(JSON.stringify(bundle)).byteLength
}

export interface SnapshotStore {
  init(): Promise<void>
  listLocal(): Promise<SnapshotMetadata[]>
  getLocal(id: string): Promise<StoredSnapshot | null>
  createSnapshot(
    bundle: SyncBundle,
    trigger: SnapshotTrigger,
    opts?: { isSessionPristine?: boolean },
  ): Promise<SnapshotMetadata>
  deleteLocal(id: string): Promise<void>
  /**
   * Strip the isSessionPristine flag from every stored snapshot. Used by the
   * session-pristine bootstrap to ensure only the snapshot from the current
   * bootstrap carries the flag; older ones keep their data but no longer
   * block compaction. Pass `exceptId` to preserve the flag on a specific
   * snapshot (used when demote runs *after* createSnapshot to keep rotation
   * atomic).
   */
  demoteSessionPristine(exceptId?: string): Promise<void>
  /**
   * Demote every existing pristine flag and write a new pristine snapshot in
   * one readwrite transaction. IndexedDB serializes `readwrite` transactions
   * on the same object store across tabs, so concurrent bootstraps cannot
   * interleave demote/create steps and leave zero pristine rows.
   */
  rotateSessionPristine(bundle: SyncBundle, trigger: SnapshotTrigger): Promise<SnapshotMetadata>
  compact(): Promise<{ kept: string[]; evicted: string[] }>
  clear(): Promise<void>
}

export function createSnapshotStore(dbName = 'purdex-sync'): SnapshotStore {
  const dbPromise = openIDB(dbName, DB_VERSION, (db) => {
    if (!db.objectStoreNames.contains(STORE)) {
      const os = db.createObjectStore(STORE, { keyPath: 'id' })
      os.createIndex('by-timestamp', 'timestamp')
    }
  })

  async function compactFn(): Promise<{ kept: string[]; evicted: string[] }> {
    const db = await dbPromise
    const all = await db.getAll(STORE) as StoredSnapshot[]
     
    const metas: SnapshotMetadata[] = all.map(({ bundle: _bundle, ...m }) => m as SnapshotMetadata)
    const { kept, evicted } = computeCompaction(metas, Date.now())
    const tx = db.transaction(STORE, 'readwrite')
    await Promise.all(evicted.map((id) => tx.store.delete(id)))
    await tx.done
    return { kept, evicted }
  }

  return {
    async init() {
      await dbPromise
    },

    async listLocal() {
      const db = await dbPromise
      const all = await db.getAll(STORE) as StoredSnapshot[]
      return all
         
        .map(({ bundle: _bundle, ...meta }) => meta as SnapshotMetadata)
        .sort((a, b) => b.timestamp - a.timestamp)
    },

    async getLocal(id) {
      const db = await dbPromise
      const row = await db.get(STORE, id)
      return (row as StoredSnapshot | undefined) ?? null
    },

    async createSnapshot(bundle, trigger, opts) {
      const db = await dbPromise
      const meta: SnapshotMetadata = {
        id: genId(),
        timestamp: Date.now(),
        device: bundle.device,
        trigger,
        bundleSize: computeBundleSize(bundle),
        contributorIds: Object.keys(bundle.collections),
        isSessionPristine: opts?.isSessionPristine ?? false,
      }
      const record: StoredSnapshot = { ...meta, bundle }
      try {
        await db.put(STORE, record)
      } catch (err) {
        // When quota is tight the initial put fails before compaction has
        // a chance to evict retention-eligible rows. Run compaction and
        // retry once before surfacing the error; without this fallback a
        // full IDB locks users out of manual / pre-import / pre-restore
        // snapshots even when old hourly rows could be dropped.
        if (!isQuotaError(err)) throw err
        await compactFn()
        await db.put(STORE, record)
      }
      await compactFn()
      return meta
    },

    async deleteLocal(id) {
      const db = await dbPromise
      await db.delete(STORE, id)
    },

    async demoteSessionPristine(exceptId) {
      const db = await dbPromise
      const all = await db.getAll(STORE) as StoredSnapshot[]
      const targets = all.filter((r) => r.isSessionPristine && r.id !== exceptId)
      if (targets.length === 0) return
      const tx = db.transaction(STORE, 'readwrite')
      await Promise.all(
        targets.map((r) => tx.store.put({ ...r, isSessionPristine: false })),
      )
      await tx.done
    },

    async rotateSessionPristine(bundle, trigger) {
      const db = await dbPromise
      const meta: SnapshotMetadata = {
        id: genId(),
        timestamp: Date.now(),
        device: bundle.device,
        trigger,
        bundleSize: computeBundleSize(bundle),
        contributorIds: Object.keys(bundle.collections),
        isSessionPristine: true,
      }
      const record: StoredSnapshot = { ...meta, bundle }

      // Single readwrite transaction: demote every existing pristine row,
      // then put the new one. IDB readwrite transactions on the same store
      // are serialized (including across tabs), so two concurrent
      // ensureSessionPristine calls cannot interleave demote/put steps and
      // end up with zero pristine rows.
      const tx = db.transaction(STORE, 'readwrite')
      const all = await tx.store.getAll() as StoredSnapshot[]
      for (const r of all) {
        if (r.isSessionPristine) {
          await tx.store.put({ ...r, isSessionPristine: false })
        }
      }
      await tx.store.put(record)
      await tx.done

      // Compaction runs outside the transaction because computeCompaction
      // needs to read metadata post-commit and its own delete pass already
      // runs in its own tx. Failures here do not undo the rotation.
      await compactFn()
      return meta
    },

    async compact() {
      return compactFn()
    },

    async clear() {
      const db = await dbPromise
      await db.clear(STORE)
    },
  }
}
