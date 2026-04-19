import { openDB, type IDBPDatabase } from 'idb'

// Cache is keyed by (name, version) so a later openIDB for the same name at
// a higher version does not silently reuse an old connection and skip the
// upgrade callback. Rejected and terminated connections are evicted so the
// next call can reconnect.
const openConnections = new Map<string, Promise<IDBPDatabase>>()

function cacheKey(name: string, version: number): string {
  return `${name}:${version}`
}

/**
 * Open (or reuse) an IndexedDB database by name + version.
 *
 * The `upgrade` callback runs only when the version changes; use it to
 * create object stores / indexes. Re-opening the same name+version returns
 * the shared cached connection. Rejected promises and connections closed
 * outside our control (tab eviction, schema upgrade in another context)
 * are dropped from the cache so the next caller reconnects.
 */
export function openIDB(
  name: string,
  version: number,
  upgrade: (db: IDBPDatabase) => void,
): Promise<IDBPDatabase> {
  const key = cacheKey(name, version)
  const cached = openConnections.get(key)
  if (cached) return cached

  const p = openDB(name, version, {
    upgrade(db) {
      upgrade(db)
    },
    blocking() {
      // Another context is upgrading this DB — release our handle so it
      // can proceed. Next caller will open a fresh connection.
      void openConnections
        .get(key)
        ?.then((db) => {
          db.close()
        })
        .catch(() => {})
      openConnections.delete(key)
    },
    terminated() {
      // Connection was closed outside our control (tab suspend, browser
      // reclaim, unexpected error). Drop so the next open reconnects.
      openConnections.delete(key)
    },
  })

  p.catch(() => {
    openConnections.delete(key)
  })

  openConnections.set(key, p)
  return p
}

/** Close all cached connections (used by tests between cases). */
export async function closeAllIDB(): Promise<void> {
  for (const p of openConnections.values()) {
    try {
      const db = await p
      db.close()
    } catch {
      // ignore
    }
  }
  openConnections.clear()
}
