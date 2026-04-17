import type { SyncBundle, SyncProvider, SyncSnapshot } from '../types'
import { hostFetch } from '../../host-api'

// ---------------------------------------------------------------------------
// DaemonProvider — REST transport via local daemon
// ---------------------------------------------------------------------------

/**
 * A SyncProvider that communicates with the Purdex daemon's sync HTTP API.
 * All requests are routed through `hostFetch` so authentication headers and
 * the correct base URL are applied automatically.
 *
 * pushChunks / pullChunks are stubs — content-addressed chunked transfer is
 * not yet implemented on the daemon side.
 */
export function createDaemonProvider(hostId: string, clientId: string): SyncProvider {
  const encoded = encodeURIComponent(clientId)
  return {
    id: 'daemon',

    async push(bundle: SyncBundle): Promise<void> {
      const res = await hostFetch(hostId, `/api/sync/push?clientId=${encoded}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(bundle),
      })
      if (!res.ok) {
        throw new Error(`sync push failed: ${res.status} ${res.statusText}`)
      }
    },

    async pull(): Promise<SyncBundle | null> {
      const res = await hostFetch(hostId, `/api/sync/pull?clientId=${encoded}`, undefined)
      if (!res.ok) {
        throw new Error(`sync pull failed: ${res.status} ${res.statusText}`)
      }
      return res.json() as Promise<SyncBundle | null>
    },

    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    async pushChunks(chunks: Record<string, Uint8Array>): Promise<void> {},

    // eslint-disable-next-line @typescript-eslint/no-unused-vars
    async pullChunks(hashes: string[]): Promise<Record<string, Uint8Array>> { return {} },

    async listHistory(limit: number): Promise<SyncSnapshot[]> {
      if (!Number.isInteger(limit) || limit < 1) {
        throw new Error(`sync listHistory: limit must be a positive integer, got ${limit}`)
      }
      const res = await hostFetch(
        hostId,
        `/api/sync/history?clientId=${encoded}&limit=${limit}`,
        undefined,
      )
      if (!res.ok) {
        throw new Error(`sync listHistory failed: ${res.status} ${res.statusText}`)
      }
      return res.json() as Promise<SyncSnapshot[]>
    },
  }
}
