import { generateId } from '../id'
import { canonicalizePath, isDescendantPath, splitParentPath } from './path-codec'
import { EDITOR_NODES_STORE } from './db'

export type EditorNodeKind = 'file' | 'folder'
export type EditorNodeState = 'active' | 'deleted' | 'orphaned'

export interface EditorTreeNode {
  id: string
  path: string
  parentPath: string
  name: string
  kind: EditorNodeKind
  docId?: string
  state: EditorNodeState
  createdAt: number
  updatedAt: number
  lastOpenedAt?: number
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
  try {
    tx.abort()
  } catch {
    // Ignore abort failures and surface the original error.
  }
  throw error
}

function createNode(path: string, kind: EditorNodeKind, docId?: string): EditorTreeNode {
  const canonical = canonicalizePath(path)
  if (canonical === '/') {
    throw new Error('Root path is reserved')
  }

  const now = Date.now()
  const { parentPath, name } = splitParentPath(canonical)
  return {
    id: docId ?? generateId(),
    path: canonical,
    parentPath,
    name,
    kind,
    docId,
    state: 'active',
    createdAt: now,
    updatedAt: now,
    lastOpenedAt: now,
  }
}

function buildDetachedPath(node: Pick<EditorTreeNode, 'id'>): string {
  return `/.purdex-deleted/${node.id}`
}

async function getByIndex<T>(store: IDBObjectStore, indexName: string, key: IDBValidKey): Promise<T | undefined> {
  return requestToPromise<T | undefined>(store.index(indexName).get(key))
}

async function getAllNodes<T>(store: IDBObjectStore): Promise<T[]> {
  return requestToPromise<T[]>(store.getAll())
}

async function ensureParentFolderExists(tx: IDBTransaction, path: string): Promise<void> {
  const { parentPath } = splitParentPath(path)
  if (parentPath === '/') {
    return
  }

  const store = tx.objectStore(EDITOR_NODES_STORE)
  const parent = await getByIndex<EditorTreeNode>(store, 'path', parentPath)
  if (!parent || parent.kind !== 'folder' || parent.state !== 'active') {
    abortTx(tx, new Error(`Parent folder does not exist: ${parentPath}`))
  }
}

function rewritePath(path: string, fromPath: string, toPath: string): string {
  return path === fromPath ? toPath : `${toPath}${path.slice(fromPath.length)}`
}

export class EditorTreeRepository {
  constructor(private db: IDBDatabase) {}

  async getNodeByPath(path: string, tx?: TxLike): Promise<EditorTreeNode | undefined> {
    const canonical = canonicalizePath(path)
    const ownsTx = tx === undefined
    const activeTx = tx ?? this.db.transaction(EDITOR_NODES_STORE, 'readonly')
    const store = activeTx.objectStore(EDITOR_NODES_STORE)
    const node = await getByIndex<EditorTreeNode>(store, 'path', canonical)
    if (ownsTx) {
      await transactionComplete(activeTx)
    }
    return node
  }

  async getNodeByDocId(docId: string, tx?: TxLike): Promise<EditorTreeNode | undefined> {
    const ownsTx = tx === undefined
    const activeTx = tx ?? this.db.transaction(EDITOR_NODES_STORE, 'readonly')
    const store = activeTx.objectStore(EDITOR_NODES_STORE)
    const node = await getByIndex<EditorTreeNode>(store, 'docId', docId)
    if (ownsTx) {
      await transactionComplete(activeTx)
    }
    return node
  }

  async listChildren(path: string, tx?: TxLike): Promise<EditorTreeNode[]> {
    const canonical = canonicalizePath(path)
    const ownsTx = tx === undefined
    const activeTx = tx ?? this.db.transaction(EDITOR_NODES_STORE, 'readonly')
    const store = activeTx.objectStore(EDITOR_NODES_STORE)
    const nodes = await requestToPromise<EditorTreeNode[]>(store.index('parentPath').getAll(canonical))
    if (ownsTx) {
      await transactionComplete(activeTx)
    }
    return nodes
      .filter((node) => node.state === 'active')
      .sort((a, b) => {
        if (a.kind !== b.kind) {
          return a.kind === 'folder' ? -1 : 1
        }
        return a.name.localeCompare(b.name)
      })
  }

  async listAllNodes(tx?: TxLike): Promise<EditorTreeNode[]> {
    const ownsTx = tx === undefined
    const activeTx = tx ?? this.db.transaction(EDITOR_NODES_STORE, 'readonly')
    const store = activeTx.objectStore(EDITOR_NODES_STORE)
    const nodes = await getAllNodes<EditorTreeNode>(store)
    if (ownsTx) {
      await transactionComplete(activeTx)
    }
    return nodes
  }

  async createFileNode(path: string, docId: string, tx?: TxLike): Promise<EditorTreeNode> {
    const canonical = canonicalizePath(path)
    const ownsTx = tx === undefined
    const activeTx = tx ?? this.db.transaction(EDITOR_NODES_STORE, 'readwrite')
    await ensureParentFolderExists(activeTx, canonical)

    const existing = await this.getNodeByPath(canonical, activeTx)
    if (existing) {
      abortTx(activeTx, new Error(`Path already exists: ${canonical}`))
    }

    const node = createNode(canonical, 'file', docId)
    activeTx.objectStore(EDITOR_NODES_STORE).put(node)
    if (ownsTx) {
      await transactionComplete(activeTx)
    }
    return node
  }

  async createFolderNode(path: string, tx?: TxLike): Promise<EditorTreeNode> {
    const canonical = canonicalizePath(path)
    if (canonical === '/') {
      throw new Error('Root folder already exists')
    }

    const ownsTx = tx === undefined
    const activeTx = tx ?? this.db.transaction(EDITOR_NODES_STORE, 'readwrite')
    await ensureParentFolderExists(activeTx, canonical)

    const existing = await this.getNodeByPath(canonical, activeTx)
    if (existing) {
      if (existing.kind !== 'folder') {
        abortTx(activeTx, new Error(`Path already exists as a file: ${canonical}`))
      }
      if (existing.state !== 'active') {
        abortTx(activeTx, new Error(`Path is inactive: ${canonical}`))
      }
      if (ownsTx) {
        await transactionComplete(activeTx)
      }
      return existing
    }

    const node = createNode(canonical, 'folder')
    activeTx.objectStore(EDITOR_NODES_STORE).put(node)
    if (ownsTx) {
      await transactionComplete(activeTx)
    }
    return node
  }

  async renameNode(fromPath: string, toPath: string, tx?: TxLike): Promise<void> {
    const canonicalFrom = canonicalizePath(fromPath)
    const canonicalTo = canonicalizePath(toPath)
    if (canonicalFrom === canonicalTo) {
      return
    }
    if (isDescendantPath(canonicalTo, canonicalFrom) && canonicalTo !== canonicalFrom) {
      throw new Error('Cannot move node into its own descendant')
    }

    const ownsTx = tx === undefined
    const activeTx = tx ?? this.db.transaction(EDITOR_NODES_STORE, 'readwrite')
    await ensureParentFolderExists(activeTx, canonicalTo)

    const source = await this.getNodeByPath(canonicalFrom, activeTx)
    if (!source) {
      abortTx(activeTx, new Error(`Path not found: ${canonicalFrom}`))
    }
    if (source.state !== 'active') {
      abortTx(activeTx, new Error(`Path is not active: ${canonicalFrom}`))
    }

    const store = activeTx.objectStore(EDITOR_NODES_STORE)
    const nodes = await getAllNodes<EditorTreeNode>(store)
    const activeNodes = nodes.filter((node) => node.state === 'active')
    const movedNodes = activeNodes.filter((node) => node.path === canonicalFrom || isDescendantPath(node.path, canonicalFrom))
    const nextPaths = new Set(movedNodes.map((node) => rewritePath(node.path, canonicalFrom, canonicalTo)))

    for (const node of activeNodes) {
      if (node.path === canonicalFrom || isDescendantPath(node.path, canonicalFrom)) {
        continue
      }
      if (nextPaths.has(node.path)) {
        abortTx(activeTx, new Error(`Path already exists: ${node.path}`))
      }
    }

    const now = Date.now()
    for (const node of movedNodes) {
      const nextPath = rewritePath(node.path, canonicalFrom, canonicalTo)
      store.put({
        ...node,
        path: nextPath,
        ...splitParentPath(nextPath),
        updatedAt: now,
      })
    }

    if (ownsTx) {
      await transactionComplete(activeTx)
    }
  }

  async markDeleted(path: string, tx?: TxLike): Promise<EditorTreeNode[]> {
    const canonical = canonicalizePath(path)
    const ownsTx = tx === undefined
    const activeTx = tx ?? this.db.transaction(EDITOR_NODES_STORE, 'readwrite')

    const source = await this.getNodeByPath(canonical, activeTx)
    if (!source) {
      abortTx(activeTx, new Error(`Path not found: ${canonical}`))
    }
    if (source.state !== 'active') {
      abortTx(activeTx, new Error(`Path is not active: ${canonical}`))
    }

    const store = activeTx.objectStore(EDITOR_NODES_STORE)
    const nodes = await getAllNodes<EditorTreeNode>(store)
    const now = Date.now()
    const affected: EditorTreeNode[] = []

    for (const node of nodes) {
      if (node.state !== 'active') {
        continue
      }
      if (node.path === canonical || isDescendantPath(node.path, canonical)) {
        const detachedPath = buildDetachedPath(node)
        const next: EditorTreeNode = {
          ...node,
          path: detachedPath,
          ...splitParentPath(detachedPath),
          state: node.path === canonical ? 'deleted' : 'orphaned',
          updatedAt: now,
        }
        affected.push(next)
        store.put(next)
      }
    }

    if (ownsTx) {
      await transactionComplete(activeTx)
    }
    return affected
  }

  async touchPath(path: string, openedAt = Date.now(), tx?: TxLike): Promise<void> {
    const canonical = canonicalizePath(path)
    const ownsTx = tx === undefined
    const activeTx = tx ?? this.db.transaction(EDITOR_NODES_STORE, 'readwrite')
    const node = await this.getNodeByPath(canonical, activeTx)
    if (!node) {
      abortTx(activeTx, new Error(`Path not found: ${canonical}`))
    }
    if (node.state !== 'active') {
      abortTx(activeTx, new Error(`Path is not active: ${canonical}`))
    }

    activeTx.objectStore(EDITOR_NODES_STORE).put({
      ...node,
      lastOpenedAt: openedAt,
      updatedAt: openedAt,
    })
    if (ownsTx) {
      await transactionComplete(activeTx)
    }
  }

  async putNode(node: EditorTreeNode, tx?: TxLike): Promise<EditorTreeNode> {
    const ownsTx = tx === undefined
    const activeTx = tx ?? this.db.transaction(EDITOR_NODES_STORE, 'readwrite')
    activeTx.objectStore(EDITOR_NODES_STORE).put(node)
    if (ownsTx) {
      await transactionComplete(activeTx)
    }
    return node
  }
}
