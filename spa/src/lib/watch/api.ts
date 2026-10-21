// spa/src/lib/watch/api.ts — `GET /api/watch/status`, through the host fetch helper.
import { hostFetch } from '../host-api'
import { parseWatchStatus, type WatchStatus } from './status-types'

export type WatchStatusResult = { kind: 'ok'; status: WatchStatus } | { kind: 'unsupported' } | { kind: 'error' }

/** 404 → `unsupported` (a daemon without the watch module); an unreadable body, an unknown state or any other failure → `error`. */
export async function fetchWatchStatus(hostId: string): Promise<WatchStatusResult> {
  try {
    const res = await hostFetch(hostId, '/api/watch/status')
    if (res.status === 404) return { kind: 'unsupported' }
    if (!res.ok) return { kind: 'error' }
    const status = parseWatchStatus(await res.json())
    return status ? { kind: 'ok', status } : { kind: 'error' }
  } catch {
    return { kind: 'error' }
  }
}
