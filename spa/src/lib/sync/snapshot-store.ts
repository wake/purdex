import { openIDB } from '../storage/idb'
import type { SyncBundle } from './types'
import type { SnapshotMetadata, SnapshotTrigger, StoredSnapshot } from './snapshot-types'

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
      await db.put(STORE, record)
      return meta
    },

    async deleteLocal(id) {
      const db = await dbPromise
      await db.delete(STORE, id)
    },

    async compact() {
      // Implemented in Task 8
      throw new Error('not implemented')
    },

    async clear() {
      const db = await dbPromise
      await db.clear(STORE)
    },
  }
}
