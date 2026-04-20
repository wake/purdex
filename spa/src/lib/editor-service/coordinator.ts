import { generateId } from '../id'
import { canonicalizePath, isDescendantPath, splitParentPath } from '../editor-db/path-codec'
import {
  EDITOR_CONTENTS_STORE,
  EDITOR_NODES_STORE,
  openEditorDb,
} from '../editor-db/db'
import { EditorTreeRepository, type EditorTreeNode } from '../editor-db/tree-repository'
import {
  EditorContentRepository,
  type EditorContentRecord,
} from '../editor-db/content-repository'

export interface EditorFileRecord {
  docId: string
  path: string
  version: number
}

export interface EditorCoordinator {
  createFile(path: string, initialContent: string): Promise<EditorFileRecord>
  createFolder(path: string): Promise<string>
  resolvePath(docId: string): Promise<string | null>
  renameNode(fromPath: string, toPath: string): Promise<void>
  saveDocument(docId: string, text: string, expectedVersion: number): Promise<EditorFileRecord>
  saveDocumentAs(docId: string, newPath: string, text: string, expectedVersion: number): Promise<EditorFileRecord>
  listRecentOpened(limit: number): Promise<Array<Pick<EditorTreeNode, 'docId' | 'path' | 'lastOpenedAt'> & { version?: number }>>
  readFile(path: string): Promise<Uint8Array>
  writeFile(path: string, content: Uint8Array): Promise<void>
  statPath(path: string): Promise<{ size: number; mtime: number; isDirectory: boolean; isFile: boolean }>
  listPath(path: string): Promise<Array<{ name: string; isDir: boolean; size: number }>>
  mkdir(path: string): Promise<void>
  deletePath(path: string): Promise<void>
}

let coordinatorPromise: Promise<EditorCoordinator> | null = null

function transactionComplete(tx: IDBTransaction): Promise<void> {
  return new Promise((resolve, reject) => {
    tx.oncomplete = () => resolve()
    tx.onerror = () => reject(tx.error ?? new Error('IndexedDB transaction failed'))
    tx.onabort = () => reject(tx.error ?? new Error('IndexedDB transaction aborted'))
  })
}

function isRecoverableCoordinatorError(error: unknown): boolean {
  const candidate = error as { name?: string; message?: string } | null
  if (!candidate) return false
  const recoverableNames = new Set([
    'AbortError',
    'InvalidStateError',
    'NotFoundError',
    'TransactionInactiveError',
    'VersionError',
  ])
  return (
    (candidate.name !== undefined && recoverableNames.has(candidate.name)) ||
    /closed|invalid state|inactive/i.test(candidate.message ?? '')
  )
}

async function ensureIndexedDb(): Promise<void> {
  if (typeof globalThis.indexedDB !== 'undefined') {
    return
  }
  await import('fake-indexeddb/auto')
}

async function withWriteTransaction<T>(
  db: IDBDatabase,
  op: (tx: IDBTransaction, tree: EditorTreeRepository, contents: EditorContentRepository) => Promise<T>,
): Promise<T> {
  const tx = db.transaction([EDITOR_NODES_STORE, EDITOR_CONTENTS_STORE], 'readwrite')
  const tree = new EditorTreeRepository(db)
  const contents = new EditorContentRepository(db)
  try {
    const result = await op(tx, tree, contents)
    await transactionComplete(tx)
    return result
  } catch (error) {
    try {
      tx.abort()
    } catch {
      // Ignore abort failures and surface the original error.
    }
    throw error
  }
}

function nextSavedRecord(
  current: EditorContentRecord,
  text: string,
  expectedVersion: number,
  path: string,
): EditorContentRecord {
  if (current.version !== expectedVersion) {
    throw new Error(`Version mismatch for ${current.docId}: expected ${expectedVersion}, got ${current.version}`)
  }

  const savedAt = Date.now()
  return {
    ...current,
    text,
    basePath: canonicalizePath(path),
    version: current.version + 1,
    savedAt,
    tombstone: false,
    bindingStatus: 'active',
  }
}

async function ensureParentFolderExists(
  tree: EditorTreeRepository,
  path: string,
  tx: IDBTransaction,
): Promise<void> {
  const { parentPath } = splitParentPath(path)
  if (parentPath === '/') {
    return
  }
  const parent = await tree.getNodeByPath(parentPath, tx)
  if (!parent || parent.kind !== 'folder' || parent.state !== 'active') {
    throw new Error(`Parent folder does not exist: ${parentPath}`)
  }
}

async function createCoordinatorFromDb(db: IDBDatabase): Promise<EditorCoordinator> {
  const tree = new EditorTreeRepository(db)
  const contents = new EditorContentRepository(db)

  return {
    async createFile(path: string, initialContent: string): Promise<EditorFileRecord> {
      const canonical = canonicalizePath(path)
      const docId = generateId()
      return withWriteTransaction(db, async (tx) => {
        await tree.createFileNode(canonical, docId, tx)
        await contents.createDocument(docId, initialContent, canonical, tx)
        return { docId, path: canonical, version: 1 }
      })
    },

    async createFolder(path: string): Promise<string> {
      const canonical = canonicalizePath(path)
      await withWriteTransaction(db, async (tx) => {
        await tree.createFolderNode(canonical, tx)
        return canonical
      })
      return canonical
    },

    async resolvePath(docId: string): Promise<string | null> {
      const [node, record] = await Promise.all([
        tree.getNodeByDocId(docId),
        contents.readDocument(docId),
      ])
      if (!node || !record) {
        return null
      }
      if (node.state !== 'active' || node.kind !== 'file') {
        return null
      }
      if (record.bindingStatus !== 'active') {
        return null
      }
      return node.path
    },

    async renameNode(fromPath: string, toPath: string): Promise<void> {
      const canonicalFrom = canonicalizePath(fromPath)
      const canonicalTo = canonicalizePath(toPath)
      await withWriteTransaction(db, async (tx) => {
        const allNodes = await tree.listAllNodes(tx)
        const activeSourceNodes = allNodes.filter((node) =>
          node.state === 'active' && (node.path === canonicalFrom || isDescendantPath(node.path, canonicalFrom)),
        )
        await tree.renameNode(canonicalFrom, canonicalTo, tx)
        for (const node of activeSourceNodes) {
          if (node.kind !== 'file' || !node.docId) {
            continue
          }
          const nextPath = node.path === canonicalFrom
            ? canonicalTo
            : `${canonicalTo}${node.path.slice(canonicalFrom.length)}`
          await contents.updateBasePath(node.docId, nextPath, tx)
        }
      })
    },

    async saveDocument(docId: string, text: string, expectedVersion: number): Promise<EditorFileRecord> {
      return withWriteTransaction(db, async (tx) => {
        const node = await tree.getNodeByDocId(docId, tx)
        const record = await contents.readDocument(docId, tx)
        if (!node || !record) {
          throw new Error(`Document not found: ${docId}`)
        }

        const desiredPath = record.basePath
        const nextRecord = nextSavedRecord(record, text, expectedVersion, desiredPath)
        if (node.state !== 'active' || record.bindingStatus !== 'active') {
          await ensureParentFolderExists(tree, desiredPath, tx)
          const conflicting = await tree.getNodeByPath(desiredPath, tx)
          if (conflicting && conflicting.docId !== docId) {
            throw new Error(`Save As required: path already exists: ${desiredPath}`)
          }
          await tree.putNode({
            ...node,
            path: desiredPath,
            ...splitParentPath(desiredPath),
            state: 'active',
            updatedAt: Date.now(),
            lastOpenedAt: Date.now(),
          }, tx)
        }

        await contents.putDocument(nextRecord, tx)
        await tree.touchPath(desiredPath, nextRecord.savedAt, tx)
        return { docId, path: desiredPath, version: nextRecord.version }
      })
    },

    async saveDocumentAs(docId: string, newPath: string, text: string, expectedVersion: number): Promise<EditorFileRecord> {
      const canonical = canonicalizePath(newPath)
      return withWriteTransaction(db, async (tx) => {
        const node = await tree.getNodeByDocId(docId, tx)
        const record = await contents.readDocument(docId, tx)
        if (!node || !record) {
          throw new Error(`Document not found: ${docId}`)
        }
        await ensureParentFolderExists(tree, canonical, tx)
        const existing = await tree.getNodeByPath(canonical, tx)
        if (existing && existing.docId !== docId) {
          throw new Error(`Path already exists: ${canonical}`)
        }
        const nextRecord = nextSavedRecord(record, text, expectedVersion, canonical)
        await tree.putNode({
          ...node,
          path: canonical,
          ...splitParentPath(canonical),
          state: 'active',
          updatedAt: nextRecord.savedAt,
          lastOpenedAt: nextRecord.savedAt,
        }, tx)
        await contents.putDocument(nextRecord, tx)
        return { docId, path: canonical, version: nextRecord.version }
      })
    },

    async listRecentOpened(limit: number) {
      const nodes = await tree.listAllNodes()
      const activeFileNodes = nodes
        .filter((node) => node.state === 'active' && node.kind === 'file' && node.docId && node.lastOpenedAt)
        .sort((a, b) => (b.lastOpenedAt ?? 0) - (a.lastOpenedAt ?? 0))
      const recent: Array<Pick<EditorTreeNode, 'docId' | 'path' | 'lastOpenedAt'> & { version?: number }> = []
      for (const node of activeFileNodes.slice(0, limit)) {
        const record = node.docId ? await contents.readDocument(node.docId) : undefined
        if (!record || record.bindingStatus !== 'active') {
          continue
        }
        recent.push({ docId: node.docId, path: node.path, lastOpenedAt: node.lastOpenedAt })
      }
      return recent
    },

    async readFile(path: string): Promise<Uint8Array> {
      const canonical = canonicalizePath(path)
      const node = await tree.getNodeByPath(canonical)
      if (!node || node.state !== 'active' || node.kind !== 'file' || !node.docId) {
        throw new Error(`Path not found: ${canonical}`)
      }
      const record = await contents.readDocument(node.docId)
      if (!record || record.bindingStatus !== 'active') {
        throw new Error(`Document is inactive: ${node.docId}`)
      }
      return new TextEncoder().encode(record.text)
    },

    async writeFile(path: string, content: Uint8Array): Promise<void> {
      const canonical = canonicalizePath(path)
      const text = new TextDecoder().decode(content)
      await withWriteTransaction(db, async (tx) => {
        const node = await tree.getNodeByPath(canonical, tx)
        if (node && node.state === 'active') {
          if (node.kind !== 'file' || !node.docId) {
            throw new Error(`Path is not a file: ${canonical}`)
          }
          const current = await contents.readDocument(node.docId, tx)
          if (!current || current.bindingStatus !== 'active') {
            throw new Error(`Document is inactive: ${node.docId}`)
          }
          await contents.writeDocument(node.docId, text, current.version, tx)
          await tree.touchPath(canonical, Date.now(), tx)
          return
        }

        const docId = generateId()
        await tree.createFileNode(canonical, docId, tx)
        await contents.createDocument(docId, text, canonical, tx)
      })
    },

    async statPath(path: string) {
      const canonical = canonicalizePath(path)
      const node = await tree.getNodeByPath(canonical)
      if (!node || node.state !== 'active') {
        throw new Error(`Path not found: ${canonical}`)
      }
      if (node.kind === 'folder') {
        return {
          size: 0,
          mtime: node.updatedAt,
          isDirectory: true,
          isFile: false,
        }
      }
      if (!node.docId) {
        throw new Error(`File node missing docId: ${node.path}`)
      }
      const record = await contents.readDocument(node.docId)
      if (!record || record.bindingStatus !== 'active') {
        throw new Error(`Document is inactive: ${node.docId}`)
      }
      return {
        size: new TextEncoder().encode(record.text).byteLength,
        mtime: record.savedAt,
        isDirectory: false,
        isFile: true,
      }
    },

    async listPath(path: string) {
      const canonical = canonicalizePath(path)
      const node = canonical === '/' ? undefined : await tree.getNodeByPath(canonical)
      if (canonical !== '/' && (!node || node.state !== 'active' || node.kind !== 'folder')) {
        throw new Error(`Path not found: ${canonical}`)
      }
      const children = await tree.listChildren(canonical)
      const entries: Array<{ name: string; isDir: boolean; size: number }> = []
      for (const child of children) {
        if (child.kind === 'folder') {
          entries.push({ name: child.name, isDir: true, size: 0 })
          continue
        }
        if (!child.docId) {
          throw new Error(`File node missing docId: ${child.path}`)
        }
        const record = await contents.readDocument(child.docId)
        if (!record || record.bindingStatus !== 'active') {
          throw new Error(`Document is inactive: ${child.docId}`)
        }
        entries.push({
          name: child.name,
          isDir: false,
          size: new TextEncoder().encode(record.text).byteLength,
        })
      }
      return entries
    },

    async mkdir(path: string): Promise<void> {
      const canonical = canonicalizePath(path)
      await withWriteTransaction(db, async (tx) => {
        await tree.createFolderNode(canonical, tx)
      })
    },

    async deletePath(path: string): Promise<void> {
      const canonical = canonicalizePath(path)
      const subtree = (await tree.listAllNodes())
        .filter((node) => node.state === 'active' && (node.path === canonical || isDescendantPath(node.path, canonical)))
      const tombstones = await Promise.all(subtree
        .filter((node) => node.kind === 'file' && !!node.docId)
        .map(async (node) => ({
          node,
          record: await contents.readDocument(node.docId!),
        })))
      if (tombstones.some(({ node, record }) => !record || record.bindingStatus !== 'active' || !node.docId)) {
        throw new Error(`Document is inactive: ${canonical}`)
      }
      const tx = db.transaction([EDITOR_NODES_STORE, EDITOR_CONTENTS_STORE], 'readwrite')
      await tree.markDeleted(canonical, tx)
      for (const { node, record } of tombstones) {
        await contents.putDocument({
          ...record!,
          tombstone: true,
          bindingStatus: node.path === canonical ? 'deleted' : 'orphaned',
          savedAt: Date.now(),
        }, tx)
      }
      await transactionComplete(tx)
    },
  }
}

async function createEditorCoordinatorOnce(): Promise<EditorCoordinator> {
  await ensureIndexedDb()
  const db = await openEditorDb({
    onVersionChange: () => {
      coordinatorPromise = null
    },
  })
  return createCoordinatorFromDb(db)
}

async function createEditorCoordinatorWithRetry(): Promise<EditorCoordinator> {
  try {
    return await createEditorCoordinatorOnce()
  } catch (firstError) {
    try {
      return await createEditorCoordinatorOnce()
    } catch (secondError) {
      throw secondError ?? firstError
    }
  }
}

export async function createEditorCoordinator(): Promise<EditorCoordinator> {
  return createEditorCoordinatorOnce()
}

export function resetEditorCoordinatorCache(): void {
  coordinatorPromise = null
}

export function isCoordinatorRecoverableError(error: unknown): boolean {
  return isRecoverableCoordinatorError(error)
}

export async function getEditorCoordinator(): Promise<EditorCoordinator> {
  if (!coordinatorPromise) {
    coordinatorPromise = createEditorCoordinatorWithRetry().catch((error) => {
      coordinatorPromise = null
      throw error
    })
  }
  return coordinatorPromise
}
