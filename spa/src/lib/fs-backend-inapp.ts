import type { FsBackend } from './fs-backend'
import type { FileStat, FileEntry } from '../types/fs'

interface StoredFile {
  path: string
  content: Uint8Array
  isDirectory: boolean
  mtime: number
}

export class InAppBackend implements FsBackend {
  id = 'inapp'
  label = 'In-App Storage'

  private store = new Map<string, StoredFile>()

  available(): boolean {
    return true
  }

  async read(path: string): Promise<Uint8Array> {
    const entry = this.store.get(path)
    if (!entry) throw new Error(`InAppBackend: file not found: ${path}`)
    if (entry.isDirectory) throw new Error(`InAppBackend: path is a directory: ${path}`)
    return entry.content
  }

  async write(path: string, content: Uint8Array): Promise<void> {
    // auto-create parent directories
    const parts = path.split('/')
    for (let i = 1; i < parts.length - 1; i++) {
      const dirPath = parts.slice(0, i + 1).join('/')
      if (!this.store.has(dirPath)) {
        this.store.set(dirPath, {
          path: dirPath,
          content: new Uint8Array(0),
          isDirectory: true,
          mtime: Date.now(),
        })
      }
    }

    this.store.set(path, {
      path,
      content,
      isDirectory: false,
      mtime: Date.now(),
    })
  }

  async stat(path: string): Promise<FileStat> {
    const entry = this.store.get(path)
    if (!entry) throw new Error(`InAppBackend: path not found: ${path}`)
    return {
      size: entry.content.byteLength,
      mtime: entry.mtime,
      isDirectory: entry.isDirectory,
      isFile: !entry.isDirectory,
    }
  }

  async list(path: string): Promise<FileEntry[]> {
    const prefix = path.endsWith('/') ? path : path + '/'
    const seen = new Map<string, FileEntry>()

    for (const [key, entry] of this.store) {
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

  async mkdir(path: string, _recursive?: boolean): Promise<void> { // eslint-disable-line @typescript-eslint/no-unused-vars
    this.store.set(path, {
      path,
      content: new Uint8Array(0),
      isDirectory: true,
      mtime: Date.now(),
    })
  }

  async delete(path: string, _recursive?: boolean): Promise<void> { // eslint-disable-line @typescript-eslint/no-unused-vars
    this.store.delete(path)
    const prefix = path.endsWith('/') ? path : path + '/'
    for (const key of this.store.keys()) {
      if (key.startsWith(prefix)) {
        this.store.delete(key)
      }
    }
  }

  async rename(from: string, to: string): Promise<void> {
    const entry = this.store.get(from)
    if (!entry) throw new Error(`InAppBackend: path not found: ${from}`)
    this.store.set(to, { ...entry, path: to })
    this.store.delete(from)
  }
}
