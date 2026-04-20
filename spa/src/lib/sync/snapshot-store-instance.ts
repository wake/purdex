import { createSnapshotStore, type SnapshotStore } from './snapshot-store'

let instance: SnapshotStore | null = null

/** Singleton accessor; tests can override via setSnapshotStore(mock). */
export function getSnapshotStore(): SnapshotStore {
  if (!instance) instance = createSnapshotStore('purdex-sync')
  return instance
}

/** @internal for tests only */
export function setSnapshotStore(store: SnapshotStore | null): void {
  instance = store
}
