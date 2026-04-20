export const EDITOR_DB_NAME = 'purdex-editor'
export const EDITOR_DB_VERSION = 1

export const EDITOR_NODES_STORE = 'editor_nodes'
export const EDITOR_CONTENTS_STORE = 'editor_contents'

export type OpenEditorDbOptions = {
  onBlocked?: (event: Event) => void
  onVersionChange?: (event: IDBVersionChangeEvent, database: IDBDatabase) => void
}

export function openEditorDb(options: OpenEditorDbOptions = {}): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(EDITOR_DB_NAME, EDITOR_DB_VERSION)

    request.onupgradeneeded = () => {
      const db = request.result
      const nodes = db.createObjectStore(EDITOR_NODES_STORE, { keyPath: 'id' })
      nodes.createIndex('path', 'path', { unique: true })
      nodes.createIndex('docId', 'docId', { unique: true })
      nodes.createIndex('parentPath', 'parentPath', { unique: false })

      db.createObjectStore(EDITOR_CONTENTS_STORE, { keyPath: 'docId' })
    }

    request.onblocked = (event) => {
      options.onBlocked?.(event)
    }

    request.onsuccess = () => {
      const database = request.result
      database.onversionchange = (event) => {
        try {
          options.onVersionChange?.(event, database)
        } finally {
          database.close()
        }
      }

      resolve(database)
    }
    request.onerror = () => reject(request.error ?? new Error('openEditorDb failed'))
  })
}
