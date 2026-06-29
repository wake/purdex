import type { IDBPDatabase } from 'idb'
import { openIDB } from './storage/idb'
import type {
  FsBackend,
  SupportsUniqueCreate,
  SupportsMutationEvents,
  SupportsReplaceTree,
  ReplaceEntry,
} from './fs-backend'
import { join } from './storage-paths'
import type { FileStat, FileEntry } from '../types/fs'

const DB_NAME = 'pdx-inapp-fs'
const DB_VERSION = 1
const STORE = 'files'

interface StoredFile {
  path: string
  content: Uint8Array
  isDirectory: boolean
  mtime: number
}

export class InAppBackend
  implements FsBackend, SupportsUniqueCreate, SupportsMutationEvents, SupportsReplaceTree
{
  id = 'inapp'
  label = 'In-App Storage'

  // Subscribers notified after every committed tree mutation (Phase 2b). The
  // persistent backup auto-trigger subscribes here so a `/buffer` edit schedules
  // a debounced backup even with the Storage pane closed. Additive — no behaviour
  // change for non-subscribers.
  private mutationListeners = new Set<() => void>()

  onMutation(cb: () => void): () => void {
    this.mutationListeners.add(cb)
    return () => this.mutationListeners.delete(cb)
  }

  private emitMutation(): void {
    for (const cb of this.mutationListeners) cb()
  }

  // Lazy, cached IDB connection. openIDB shares the connection per (name,
  // version) so multiple InAppBackend instances reuse a single handle; tests
  // call closeAllIDB() to drop it and simulate a process restart.
  private db(): Promise<IDBPDatabase> {
    return openIDB(DB_NAME, DB_VERSION, (db) => {
      // Guard with contains() to match the repo's existing IDB upgrade style
      // (snapshot-store.ts) and stay safe across future schema version bumps.
      if (!db.objectStoreNames.contains(STORE)) {
        db.createObjectStore(STORE, { keyPath: 'path' })
      }
    })
  }

  available(): boolean {
    return true
  }

  async read(path: string): Promise<Uint8Array> {
    const db = await this.db()
    const entry = (await db.get(STORE, path)) as StoredFile | undefined
    if (!entry) throw new Error(`InAppBackend: file not found: ${path}`)
    if (entry.isDirectory) throw new Error(`InAppBackend: path is a directory: ${path}`)
    return entry.content
  }

  async write(path: string, content: Uint8Array): Promise<void> {
    const db = await this.db()
    const now = Date.now()
    // Single readwrite transaction (I5): auto-create each parent dir entry that
    // is missing, then write the file. Parent type is NOT validated (I6).
    const tx = db.transaction(STORE, 'readwrite')
    const store = tx.store
    const parts = path.split('/')
    for (let i = 1; i < parts.length - 1; i++) {
      const dirPath = parts.slice(0, i + 1).join('/')
      const existing = (await store.get(dirPath)) as StoredFile | undefined
      if (!existing) {
        await store.put({
          path: dirPath,
          content: new Uint8Array(0),
          isDirectory: true,
          mtime: now,
        } satisfies StoredFile)
      }
    }
    await store.put({
      path,
      content,
      isDirectory: false,
      mtime: now,
    } satisfies StoredFile)
    await tx.done
    this.emitMutation()
  }

  async stat(path: string): Promise<FileStat> {
    const db = await this.db()
    const entry = (await db.get(STORE, path)) as StoredFile | undefined
    if (!entry) throw new Error(`InAppBackend: path not found: ${path}`)
    return {
      size: entry.content.byteLength,
      mtime: entry.mtime,
      isDirectory: entry.isDirectory,
      isFile: !entry.isDirectory,
    }
  }

  async list(path: string): Promise<FileEntry[]> {
    const db = await this.db()
    const all = (await db.getAll(STORE)) as StoredFile[]
    const prefix = path.endsWith('/') ? path : path + '/'
    const seen = new Map<string, FileEntry>()

    for (const entry of all) {
      const key = entry.path
      if (!key.startsWith(prefix)) continue
      const rest = key.slice(prefix.length)
      // only direct children (no additional slash)
      if (rest.includes('/')) continue
      if (!seen.has(rest)) {
        seen.set(rest, {
          name: rest,
          isDir: entry.isDirectory,
          size: entry.content.byteLength,
        })
      }
    }

    return Array.from(seen.values()).sort((a, b) => {
      if (a.isDir !== b.isDir) return a.isDir ? -1 : 1
      return a.name.localeCompare(b.name)
    })
  }

  async mkdir(path: string, _recursive?: boolean): Promise<void> {
    const db = await this.db()
    // Blind overwrite if path already exists (I6).
    await db.put(STORE, {
      path,
      content: new Uint8Array(0),
      isDirectory: true,
      mtime: Date.now(),
    } satisfies StoredFile)
    this.emitMutation()
  }

  async delete(path: string, _recursive?: boolean): Promise<void> {
    const db = await this.db()
    const tx = db.transaction(STORE, 'readwrite')
    const store = tx.store
    await store.delete(path)
    // Recursively delete prefix children.
    const prefix = path.endsWith('/') ? path : path + '/'
    const keys = (await store.getAllKeys()) as string[]
    for (const key of keys) {
      if (key.startsWith(prefix)) {
        await store.delete(key)
      }
    }
    await tx.done
    this.emitMutation()
  }

  async rename(from: string, to: string): Promise<void> {
    const db = await this.db()
    const tx = db.transaction(STORE, 'readwrite')
    const store = tx.store

    // Dir-aware recursive move: select `from` itself plus every descendant
    // (`from/`-prefixed key). A file move is the single-key case of this.
    const fromPrefix = from + '/'
    const allKeys = (await store.getAllKeys()) as string[]
    const sourceKeys = allKeys.filter(
      (k) => k === from || k.startsWith(fromPrefix),
    )
    if (sourceKeys.length === 0) {
      await tx.done
      throw new Error(`InAppBackend: path not found: ${from}`)
    }

    // Full collision scan over the WHOLE target subtree BEFORE any mutation:
    // reject if `to` exists or any `${to}/`-prefixed key exists (spec AC-1b —
    // refused before any backend mutation). idb auto-aborts the tx on throw.
    const toPrefix = to + '/'
    const collision = allKeys.some(
      (k) => k === to || k.startsWith(toPrefix),
    )
    if (collision) {
      await tx.done
      throw new Error(`InAppBackend: rename target already exists: ${to}`)
    }

    // Re-key every entry in one transaction, preserving each entry's mtime
    // (a move is not a content edit, so we must NOT bump mtime).
    for (const key of sourceKeys) {
      const entry = (await store.get(key)) as StoredFile | undefined
      if (!entry) continue
      const newKey = to + key.slice(from.length)
      await store.put({ ...entry, path: newKey } satisfies StoredFile)
      await store.delete(key)
    }
    await tx.done
    this.emitMutation()
  }

  async createUnique(
    dir: string,
    baseName = 'Untitled',
    ext: string,
    content?: Uint8Array,
  ): Promise<string> {
    const db = await this.db()
    const now = Date.now()
    const MAX = 10_000
    // The reserved file's bytes: the caller-supplied `content` (OS-file upload,
    // Phase 1c) or an empty buffer (1b's new-file namer). Reused across retries —
    // only the winning add() persists, and idb structured-clones the value, so
    // sharing the reference is safe.
    const bytes = content ?? new Uint8Array(0)
    // store.add() is the single serialization point: it throws ConstraintError
    // if the keyPath (path) already exists, unlike put()'s blind overwrite. So
    // concurrent callers — e.g. a rapid double "New file" click (#854) — each
    // win exactly one distinct key; the loser retries the next suffix instead
    // of sharing the key. A fresh tx per attempt because a ConstraintError
    // aborts the transaction it occurred in.
    for (let n = 0; n < MAX; n++) {
      const name = n === 0 ? baseName : `${baseName}-${n}`
      // An ext-less name (`README`, ext === '') must not carry a trailing dot.
      const fileName = ext === '' ? name : `${name}.${ext}`
      const path = join(dir, fileName)
      const tx = db.transaction(STORE, 'readwrite')
      try {
        await tx.store.add({
          path,
          content: bytes,
          isDirectory: false,
          mtime: now,
        } satisfies StoredFile)
        await tx.done
        this.emitMutation()
        return path
      } catch (err) {
        // A failed add() aborts its transaction, so tx.done rejects with an
        // AbortError. We left it un-awaited (we threw out of the try first), so
        // observe it here to avoid an unhandled rejection before retrying.
        tx.done.catch(() => {})
        if ((err as { name?: string } | null)?.name === 'ConstraintError') continue
        throw err
      }
    }
    throw new Error(`InAppBackend.createUnique: exhausted ${MAX} candidates under ${dir}`)
  }

  async mkdirUnique(dir: string, baseName = 'New Folder'): Promise<string> {
    const db = await this.db()
    const now = Date.now()
    const MAX = 10_000
    // Symmetric with createUnique (decision 7): store.add() is the single
    // serialization point, so a rapid double "New Folder" click each wins a
    // distinct key instead of clobbering. Folder candidates use a SPACE suffix
    // ("New Folder", "New Folder 1", …) and carry NO extension. A fresh tx per
    // attempt because a ConstraintError aborts the transaction it occurred in.
    for (let n = 0; n < MAX; n++) {
      const name = n === 0 ? baseName : `${baseName} ${n}`
      const path = join(dir, name)
      const tx = db.transaction(STORE, 'readwrite')
      try {
        await tx.store.add({
          path,
          content: new Uint8Array(0),
          isDirectory: true,
          mtime: now,
        } satisfies StoredFile)
        await tx.done
        this.emitMutation()
        return path
      } catch (err) {
        // Observe the aborted tx's rejection to avoid an unhandled rejection
        // before retrying the next suffix.
        tx.done.catch(() => {})
        if ((err as { name?: string } | null)?.name === 'ConstraintError') continue
        throw err
      }
    }
    throw new Error(`InAppBackend.mkdirUnique: exhausted ${MAX} candidates under ${dir}`)
  }

  /**
   * Atomically replace everything under `root` with `entries` (Phase 2c
   * restore). Re-validates each root-relative `relPath` BEFORE opening the
   * transaction (defence-in-depth, §4.2), then in ONE readwrite txn: (a) clears
   * the `root` subtree, (b) writes dir entries first so empty dirs survive (C2),
   * (c) writes file entries. Because clear + write share one txn, any failure
   * aborts it → IndexedDB rolls back, so the prior tree is never left
   * half-cleared (R2-Pa). Deliberately does NOT emit `onMutation`: restore must
   * not re-trigger an auto-backup (consistent with 2b's exclusion).
   */
  async replaceTree(root: string, entries: ReplaceEntry[]): Promise<void> {
    // (0) Validate every relPath up-front. A bad entry rejects before any
    // mutation so a malformed manifest can never corrupt the tree.
    const seen = new Set<string>()
    const planned = entries.map((e) => {
      const rel = e.relPath
      if (typeof rel !== 'string' || rel === '') {
        throw new Error(`InAppBackend.replaceTree: empty relPath`)
      }
      if (rel.startsWith('/') || rel.includes('\\')) {
        throw new Error(`InAppBackend.replaceTree: relPath must be root-relative: ${rel}`)
      }
      const segs = rel.split('/')
      if (segs.some((s) => s === '' || s === '.' || s === '..')) {
        throw new Error(`InAppBackend.replaceTree: illegal segment in relPath: ${rel}`)
      }
      if (seen.has(rel)) {
        throw new Error(`InAppBackend.replaceTree: duplicate relPath: ${rel}`)
      }
      seen.add(rel)
      return { rel, isDir: e.isDir, bytes: e.bytes ?? new Uint8Array(0) }
    })
    // Prefix-conflict: a file entry must not be an ancestor directory of another
    // entry. (Listed dir entries are fine — that is the normal nesting case.)
    const kindByRel = new Map(planned.map((p) => [p.rel, p.isDir]))
    for (const p of planned) {
      const segs = p.rel.split('/')
      for (let i = 1; i < segs.length; i++) {
        const ancestor = segs.slice(0, i).join('/')
        if (kindByRel.get(ancestor) === false) {
          throw new Error(
            `InAppBackend.replaceTree: ${ancestor} is a file but used as a directory of ${p.rel}`,
          )
        }
      }
    }

    const db = await this.db()
    const now = Date.now()
    const tx = db.transaction(STORE, 'readwrite')
    try {
      const store = tx.store
      // (a) Clear the whole root subtree (strict children, prefix match).
      const prefix = root.endsWith('/') ? root : root + '/'
      const keys = (await store.getAllKeys()) as string[]
      for (const key of keys) {
        if (key.startsWith(prefix)) await store.delete(key)
      }
      // (b) Dirs first so a deeper file's parent already exists and empty dirs
      // round-trip; (c) then files.
      for (const p of planned) {
        if (!p.isDir) continue
        await store.put({
          path: join(root, p.rel),
          content: new Uint8Array(0),
          isDirectory: true,
          mtime: now,
        } satisfies StoredFile)
      }
      for (const p of planned) {
        if (p.isDir) continue
        await store.put({
          path: join(root, p.rel),
          content: p.bytes,
          isDirectory: false,
          mtime: now,
        } satisfies StoredFile)
      }
      await tx.done
    } catch (err) {
      // Abort so the queued deletes/puts roll back rather than auto-committing
      // when the (still-open) txn goes idle; observe the abort's rejection.
      try {
        tx.abort()
      } catch {
        // already aborting / aborted
      }
      tx.done.catch(() => {})
      throw err
    }
    // Intentionally NO emitMutation() — see method doc.
  }
}
