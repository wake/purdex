import { openDB, type IDBPDatabase } from 'idb'

const openConnections = new Map<string, Promise<IDBPDatabase>>()

/**
 * Open (or reuse) an IndexedDB database by name.
 *
 * The `upgrade` callback runs only when the version changes; use it to
 * create object stores / indexes. Re-opening the same name returns the
 * shared cached connection.
 */
export function openIDB(
  name: string,
  version: number,
  upgrade: (db: IDBPDatabase) => void,
): Promise<IDBPDatabase> {
  const cached = openConnections.get(name)
  if (cached) return cached

  const p = openDB(name, version, {
    upgrade(db) {
      upgrade(db)
    },
  })
  openConnections.set(name, p)
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
