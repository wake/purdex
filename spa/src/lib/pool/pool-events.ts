// spa/src/lib/pool/pool-events.ts — the daemon's `pool.changed` host event (#3479, `pool.events.v1`): the pool's hosts moved. It carries nothing
// the App needs: the pool store reads `GET /api/pool/hosts` again, and only when the daemon it came from is the store's source.
// Called from useMultiHostEventWs with the per-host closure's hostId, after its connection-key check.
import { usePoolStore } from './store'

export const POOL_EVENT_TYPE = 'pool.changed'
export const isPoolEvent = (type: string): boolean => type === POOL_EVENT_TYPE

export function handlePoolEvent(hostId: string): void {
  usePoolStore.getState().poolEvent(hostId)
}
