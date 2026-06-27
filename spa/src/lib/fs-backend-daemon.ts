import type { FsBackend } from './fs-backend'
import type { FileStat, FileEntry } from '../types/fs'
import { useHostStore } from '../stores/useHostStore'

export class DaemonBackend implements FsBackend {
  readonly id = 'daemon'
  readonly label = 'Remote Host'
  private readonly baseUrl: string
  private readonly getHeaders: () => Record<string, string>

  constructor(baseUrl: string, getHeaders: () => Record<string, string>) {
    this.baseUrl = baseUrl
    this.getHeaders = getHeaders
  }

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
      throw Object.assign(new Error(text), { status: res.status })
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

  // In-App-only flow (#854); no daemon endpoint / UI entry point uses it.
  async createUnique(_dir: string, _baseName: string, _ext: 'md' | 'txt'): Promise<string> {
    throw new Error('DaemonBackend: createUnique is not supported')
  }
}

/**
 * Build an `FsBackend` permanently bound to `hostId`.
 *
 * Contrast with the active-host proxy in `register-modules/fs-backends.tsx`
 * which intentionally re-resolves the active host on every call. The
 * file-open pipeline (P5) needs the opposite guarantee: once `tryOpenFile`
 * captures `ctx.hostId`, every subsequent `stat` along that flow MUST stay
 * on that host even if the user switches active host mid-flight (Deviation 2
 * + attack-critical C5). Each `getDaemon()` call still re-reads
 * `useHostStore` so daemon-base / auth-header changes for *that* host stay
 * picked up.
 */
export function createDaemonBackendForHost(hostId: string): FsBackend {
  const getDaemon = (): DaemonBackend => {
    const state = useHostStore.getState()
    return new DaemonBackend(state.getDaemonBase(hostId), () => state.getAuthHeaders(hostId))
  }
  return {
    id: 'daemon',
    label: 'Remote Host',
    available: () => !!useHostStore.getState().hosts[hostId],
    read: (path) => getDaemon().read(path),
    write: (path, content) => getDaemon().write(path, content),
    stat: (path) => getDaemon().stat(path),
    list: (path) => getDaemon().list(path),
    mkdir: (path, recursive) => getDaemon().mkdir(path, recursive),
    delete: (path, recursive) => getDaemon().delete(path, recursive),
    rename: (from, to) => getDaemon().rename(from, to),
    createUnique: (dir, baseName, ext) => getDaemon().createUnique(dir, baseName, ext),
  }
}
