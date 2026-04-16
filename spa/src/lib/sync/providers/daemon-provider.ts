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
  return {
    id: 'daemon',

    async push(bundle: SyncBundle): Promise<void> {
      const res = await hostFetch(hostId, `/api/sync/push?clientId=${clientId}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(bundle),
      })
      if (!res.ok) {
        throw new Error(`sync push failed: ${res.status} ${res.statusText}`)
      }
    },

    async pull(): Promise<SyncBundle | null> {
      const res = await hostFetch(hostId, `/api/sync/pull?clientId=${clientId}`, undefined)
      if (!res.ok) {
        throw new Error(`sync pull failed: ${res.status} ${res.statusText}`)
      }
      return res.json() as Promise<SyncBundle | null>
    },

    async pushChunks(_chunks: Record<string, Uint8Array>): Promise<void> {
      // stub — not implemented yet
    },

    async pullChunks(_hashes: string[]): Promise<Record<string, Uint8Array>> {
      // stub — not implemented yet
      return {}
    },

    async listHistory(limit: number): Promise<SyncSnapshot[]> {
      const res = await hostFetch(
        hostId,
        `/api/sync/history?clientId=${clientId}&limit=${limit}`,
        undefined,
      )
      if (!res.ok) {
        throw new Error(`sync listHistory failed: ${res.status} ${res.statusText}`)
      }
      return res.json() as Promise<SyncSnapshot[]>
    },
  }
}
