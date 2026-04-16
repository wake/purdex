import type { FsBackend } from './fs-backend'
import type { FileStat, FileEntry } from '../types/fs'

export class DaemonBackend implements FsBackend {
  readonly id = 'daemon'
  readonly label = 'Remote Host'

  constructor(
    private baseUrl: string,
    private getHeaders: () => Record<string, string>,
  ) {}

  available(): boolean {
    return !!this.baseUrl
  }

  private async post(endpoint: string, body: unknown): Promise<Response> {
    const res = await fetch(`${this.baseUrl}${endpoint}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', ...this.getHeaders() },
      body: JSON.stringify(body),
    })
    if (!res.ok) {
      const text = await res.text().catch(() => `HTTP ${res.status}`)
      throw new Error(text)
    }
    return res
  }

  async read(path: string): Promise<Uint8Array> {
    const res = await this.post('/api/fs/read', { path })
    const buf = await res.arrayBuffer()
    return new Uint8Array(buf)
  }

  async write(path: string, content: Uint8Array): Promise<void> {
    let binary = ''
    const chunkSize = 8192
    for (let i = 0; i < content.length; i += chunkSize) {
      binary += String.fromCharCode(...content.subarray(i, i + chunkSize))
    }
    const base64 = btoa(binary)
    await this.post('/api/fs/write', { path, content: base64 })
  }

  async stat(path: string): Promise<FileStat> {
    const res = await this.post('/api/fs/stat', { path })
    return res.json() as Promise<FileStat>
  }

  async list(path: string): Promise<FileEntry[]> {
    const res = await this.post('/api/fs/list', { path })
    const data = await res.json() as { path: string; entries: FileEntry[] }
    return data.entries
  }

  async mkdir(path: string, recursive?: boolean): Promise<void> {
    await this.post('/api/fs/mkdir', { path, recursive: recursive ?? false })
  }

  async delete(path: string, recursive?: boolean): Promise<void> {
    await this.post('/api/fs/delete', { path, recursive: recursive ?? false })
  }

  async rename(from: string, to: string): Promise<void> {
    await this.post('/api/fs/rename', { from, to })
  }
}
