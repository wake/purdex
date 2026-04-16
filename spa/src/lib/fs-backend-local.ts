import type { FsBackend } from './fs-backend'
import type { FileStat, FileEntry } from '../types/fs'

export class LocalBackend implements FsBackend {
  readonly id = 'local'
  readonly label = 'Local Files'

  available(): boolean {
    return !!window.electronAPI?.fs
  }

  private get api() {
    const api = window.electronAPI?.fs
    if (!api) throw new Error('Local filesystem not available (requires Electron)')
    return api
  }

  async read(path: string): Promise<Uint8Array> {
    return this.api.read(path)
  }

  async write(path: string, content: Uint8Array): Promise<void> {
    return this.api.write(path, content)
  }

  async stat(path: string): Promise<FileStat> {
    return this.api.stat(path)
  }

  async list(path: string): Promise<FileEntry[]> {
    return this.api.list(path)
  }

  async mkdir(path: string, recursive?: boolean): Promise<void> {
    return this.api.mkdir(path, recursive ?? false)
  }

  async delete(path: string, recursive?: boolean): Promise<void> {
    return this.api.delete(path, recursive ?? false)
  }

  async rename(from: string, to: string): Promise<void> {
    return this.api.rename(from, to)
  }
}
