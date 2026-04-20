import { canonicalizePath } from './path-codec'
import { EDITOR_CONTENTS_STORE } from './db'
import type { EditorNodeState } from './tree-repository'

export type EditorBindingStatus = EditorNodeState

export interface EditorContentRecord {
  docId: string
  text: string
  basePath: string
  version: number
  savedAt: number
  tombstone: boolean
  bindingStatus: EditorBindingStatus
}

type TxLike = IDBTransaction | undefined

function requestToPromise<T>(request: IDBRequest<T>): Promise<T> {
  return new Promise((resolve, reject) => {
    request.onsuccess = () => resolve(request.result)
    request.onerror = () => reject(request.error ?? new Error('IndexedDB request failed'))
  })
}

function transactionComplete(tx: IDBTransaction): Promise<void> {
  return new Promise((resolve, reject) => {
    tx.oncomplete = () => resolve()
    tx.onerror = () => reject(tx.error ?? new Error('IndexedDB transaction failed'))
    tx.onabort = () => reject(tx.error ?? new Error('IndexedDB transaction aborted'))
  })
}

function abortTx(tx: IDBTransaction, error: Error): never {
  throw error
}

export class EditorContentRepository {
  constructor(private db: IDBDatabase) {}

  async createDocument(docId: string, text: string, basePath: string, tx?: TxLike): Promise<EditorContentRecord> {
    const record: EditorContentRecord = {
      docId,
      text,
      basePath: canonicalizePath(basePath),
      version: 1,
      savedAt: Date.now(),
      tombstone: false,
      bindingStatus: 'active',
    }

    const ownsTx = tx === undefined
    const activeTx = tx ?? this.db.transaction(EDITOR_CONTENTS_STORE, 'readwrite')
    const store = activeTx.objectStore(EDITOR_CONTENTS_STORE)
    const existing = await requestToPromise<EditorContentRecord | undefined>(store.get(docId))
    if (existing) {
      abortTx(activeTx, new Error(`Document already exists: ${docId}`))
    }
    store.put(record)
    if (ownsTx) {
      await transactionComplete(activeTx)
    }
    return record
  }

  async readDocument(docId: string, tx?: TxLike): Promise<EditorContentRecord | undefined> {
    const ownsTx = tx === undefined
    const activeTx = tx ?? this.db.transaction(EDITOR_CONTENTS_STORE, 'readonly')
    const store = activeTx.objectStore(EDITOR_CONTENTS_STORE)
    const record = await requestToPromise<EditorContentRecord | undefined>(store.get(docId))
    if (ownsTx) {
      await transactionComplete(activeTx)
    }
    return record
  }

  async writeDocument(docId: string, text: string, expectedVersion: number, tx?: TxLike): Promise<EditorContentRecord> {
    const ownsTx = tx === undefined
    const activeTx = tx ?? this.db.transaction(EDITOR_CONTENTS_STORE, 'readwrite')
    const store = activeTx.objectStore(EDITOR_CONTENTS_STORE)
    const current = await requestToPromise<EditorContentRecord | undefined>(store.get(docId))
    if (!current) {
      abortTx(activeTx, new Error(`Document not found: ${docId}`))
    }
    if (current.tombstone || current.bindingStatus !== 'active') {
      abortTx(activeTx, new Error(`Document binding is inactive: ${docId}`))
    }
    if (current.version !== expectedVersion) {
      abortTx(activeTx, new Error(`Version mismatch for ${docId}: expected ${expectedVersion}, got ${current.version}`))
    }

    const next: EditorContentRecord = {
      ...current,
      text,
      basePath: canonicalizePath(current.basePath),
      version: current.version + 1,
      savedAt: Date.now(),
      tombstone: false,
      bindingStatus: 'active',
    }
    store.put(next)
    if (ownsTx) {
      await transactionComplete(activeTx)
    }
    return next
  }

  async putDocument(record: EditorContentRecord, tx?: TxLike): Promise<EditorContentRecord> {
    const ownsTx = tx === undefined
    const activeTx = tx ?? this.db.transaction(EDITOR_CONTENTS_STORE, 'readwrite')
    activeTx.objectStore(EDITOR_CONTENTS_STORE).put({
      ...record,
      basePath: canonicalizePath(record.basePath),
    })
    if (ownsTx) {
      await transactionComplete(activeTx)
    }
    return record
  }

  async updateBasePath(docId: string, basePath: string, tx?: TxLike): Promise<EditorContentRecord> {
    const ownsTx = tx === undefined
    const activeTx = tx ?? this.db.transaction(EDITOR_CONTENTS_STORE, 'readwrite')
    const current = await this.readDocument(docId, activeTx)
    if (!current) {
      abortTx(activeTx, new Error(`Document not found: ${docId}`))
    }
    if (current.tombstone || current.bindingStatus !== 'active') {
      abortTx(activeTx, new Error(`Document binding is inactive: ${docId}`))
    }
    const next: EditorContentRecord = {
      ...current,
      basePath: canonicalizePath(basePath),
      savedAt: Date.now(),
    }
    await this.putDocument(next, activeTx)
    if (ownsTx) {
      await transactionComplete(activeTx)
    }
    return next
  }

  async markTombstone(docId: string, bindingStatus: Exclude<EditorBindingStatus, 'active'> = 'deleted'): Promise<EditorContentRecord> {
    const tx = this.db.transaction(EDITOR_CONTENTS_STORE, 'readwrite')
    const current = await this.readDocument(docId, tx)
    if (!current) {
      abortTx(tx, new Error(`Document not found: ${docId}`))
    }
    const next: EditorContentRecord = {
      ...current,
      tombstone: true,
      bindingStatus,
      savedAt: Date.now(),
    }
    await this.putDocument(next, tx)
    await transactionComplete(tx)
    return next
  }
}
