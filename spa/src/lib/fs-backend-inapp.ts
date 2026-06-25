import type { IDBPDatabase } from 'idb'
import { openIDB } from './storage/idb'
import type { FsBackend } from './fs-backend'
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

export class InAppBackend implements FsBackend {
  id = 'inapp'
  label = 'In-App Storage'

  // Lazy, cached IDB connection. openIDB shares the connection per (name,
  // version) so multiple InAppBackend instances reuse a single handle; tests
  // call closeAllIDB() to drop it and simulate a process restart.
  private db(): Promise<IDBPDatabase> {
    return openIDB(DB_NAME, DB_VERSION, (db) => {
      db.createObjectStore(STORE, { keyPath: 'path' })
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
  }

  async rename(from: string, to: string): Promise<void> {
    const db = await this.db()
    const tx = db.transaction(STORE, 'readwrite')
    const store = tx.store
    const entry = (await store.get(from)) as StoredFile | undefined
    if (!entry) {
      await tx.done
      throw new Error(`InAppBackend: path not found: ${from}`)
    }
    // Blind overwrite of target (I6); only the single entry moves (I7).
    await store.put({ ...entry, path: to } satisfies StoredFile)
    await store.delete(from)
    await tx.done
  }
}
