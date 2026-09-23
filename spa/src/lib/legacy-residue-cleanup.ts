import { hasPersistedClientId } from './client-identity'
import { STORAGE_KEYS } from './storage/keys'

// ---------------------------------------------------------------------------
// Legacy residue cleanup (#1303)
//
// The old Sync module (P4a) and the device-state / workspace-snapshot features (P4b) were deleted, but the data
// they wrote stays in every browser that ran them. Nothing opens or reads any of it any more; this removes it once
// per boot. Every step is idempotent (removing a missing key or database is a no-op), so there is no "done" marker.
//
// Boot never waits on it and it never throws or rejects: failures are collected and logged ONCE.
//
// NOT residue: localStorage `purdex-device-state` (useDeviceNameStore) and the `purdex-sync` BroadcastChannel
// (lib/storage/sync.ts) — both live, they only share a name.
// ---------------------------------------------------------------------------

/** Written by the deleted lib/snapshot (P4b); nothing reads them. */
export const LEGACY_LOCAL_STORAGE_KEYS = ['purdex-workspace-snapshot', 'purdex-workspace-snapshot-prev'] as const

/** The deleted Sync module's snapshot store (object store `snapshots`); nothing opens it. */
export const LEGACY_IDB_NAME = 'purdex-sync'

function deleteLegacyDatabase(): Promise<unknown> {
  // `typeof` rather than a plain read: the global may be missing altogether (some embedded / test realms).
  if (typeof indexedDB === 'undefined' || !indexedDB) return Promise.resolve(null)
  return new Promise((resolve) => {
    try {
      const req = indexedDB.deleteDatabase(LEGACY_IDB_NAME)
      req.onsuccess = () => resolve(null)
      req.onerror = () => resolve(req.error ?? new Error(`deleteDatabase(${LEGACY_IDB_NAME}) failed`))
      // Nothing in this app opens it, so a blocked delete only means another (old) tab still has it; the delete
      // completes when that one closes. Not a failure, and not worth waiting for.
      req.onblocked = () => resolve(null)
    } catch (err) {
      resolve(err)
    }
  })
}

/** Removes the residue. Resolves when done; never rejects. */
export async function cleanupLegacyResidue(): Promise<void> {
  const failures: unknown[] = []
  const remove = (key: string) => {
    try {
      // Raw localStorage, not browserStorage: no store is registered for these keys, so a syncManager broadcast
      // would only be noise for the other windows.
      localStorage.removeItem(key)
    } catch (err) {
      failures.push(err)
    }
  }

  for (const key of LEGACY_LOCAL_STORAGE_KEYS) remove(key)

  // The old Sync store's key is still the client-id adoption source (client-identity.ts readLegacySyncClientId)
  // for an install whose id has no key of its own yet — drop it only after the id has moved.
  if (hasPersistedClientId()) remove(STORAGE_KEYS.SYNC_STATE)

  const dbFailure = await deleteLegacyDatabase()
  if (dbFailure) failures.push(dbFailure)

  if (failures.length > 0) {
    console.warn('[purdex] legacy residue cleanup: some leftovers could not be removed', failures)
  }
}

/** Boot entry: runs the cleanup after the current task (the first render goes first); nothing awaits it. */
export function scheduleLegacyResidueCleanup(): void {
  setTimeout(() => {
    void cleanupLegacyResidue().catch(() => {})
  }, 0)
}
