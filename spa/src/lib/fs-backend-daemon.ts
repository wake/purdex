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

  // The unique-name reservation (#854) is an In-App-only capability
  // (`SupportsUniqueCreate`); DaemonBackend deliberately does NOT implement it
  // (codex H1) — no daemon endpoint / UI entry point uses it.
}

/**
 * Build an `FsBackend` permanently bound to `hostId`.
 *
 * Contrast with the active-host proxy in `register-modules/fs-backends.tsx`
 * which intentionally re-resolves the active host on every call. The
 * file-open pipeline (P5) needs the opposite guarantee: once `tryOpenFile`
 * captures `ctx.hostId`, every subsequent `stat` along that flow MUST stay
 * on that host even if the user switches active host mid-flight (Deviation 2
 * + attack-critical C5). Each `withDaemon()` call still re-reads
 * `useHostStore` so daemon-base / auth-header changes for *that* host stay
 * picked up.
 *
 * When the host is gone, every operation FAILS rather than being answered by
 * some other machine. `getDaemonBase` resolves an unknown host to the active
 * one, so without this guard a backend bound to a removed host would read — and
 * write — the same path on a different machine, letting hostA vouch for hostB's
 * paths. `getFsBackend`'s resolver refuses the same case, but the file-open and
 * recent-file pipelines build their backend through this helper directly, so
 * the guard has to live here rather than at each call site. The failure is an
 * async rejection (never a synchronous throw) so the existing `try` / `.catch`
 * around those awaits keeps catching it, and it carries no `code` / `status` —
 * `isNotFoundError` must not mistake a gone host for a missing file.
 */
export function createDaemonBackendForHost(hostId: string): FsBackend {
  const withDaemon = async <T>(run: (daemon: DaemonBackend) => Promise<T>): Promise<T> => {
    const state = useHostStore.getState()
    if (!state.hosts[hostId]) {
      throw new Error(`Host ${hostId} is no longer configured`)
    }
    return run(new DaemonBackend(state.getDaemonBase(hostId), () => state.getAuthHeaders(hostId)))
  }
  return {
    id: 'daemon',
    label: 'Remote Host',
    available: () => !!useHostStore.getState().hosts[hostId],
    read: (path) => withDaemon((daemon) => daemon.read(path)),
    write: (path, content) => withDaemon((daemon) => daemon.write(path, content)),
    stat: (path) => withDaemon((daemon) => daemon.stat(path)),
    list: (path) => withDaemon((daemon) => daemon.list(path)),
    mkdir: (path, recursive) => withDaemon((daemon) => daemon.mkdir(path, recursive)),
    delete: (path, recursive) => withDaemon((daemon) => daemon.delete(path, recursive)),
    rename: (from, to) => withDaemon((daemon) => daemon.rename(from, to)),
  }
}
