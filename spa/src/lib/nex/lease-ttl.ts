// spa/src/lib/nex/lease-ttl.ts — the lease TTL a host advertises in
// GET /v1/capabilities, cached per host for the life of the page. The renew
// cadence (ttl/3) and the idle window (2×ttl) both derive from it.
import { fetchNexCapabilities } from './nex-api'

export const DEFAULT_LEASE_TTL_S = 120

const cache = new Map<string, number>()
const inflight = new Map<string, Promise<number>>()

export function getLeaseTtlSeconds(hostId: string): Promise<number> {
  const hit = cache.get(hostId)
  if (hit != null) return Promise.resolve(hit)
  const pending = inflight.get(hostId)
  if (pending) return pending
  const p = fetchNexCapabilities(hostId)
    .then((caps) => {
      const ttl = caps.lease?.ttl_seconds
      const value = typeof ttl === 'number' && ttl > 0 ? ttl : DEFAULT_LEASE_TTL_S
      cache.set(hostId, value)
      return value
    })
    .catch(() => DEFAULT_LEASE_TTL_S) // not cached: the next call asks again
    .finally(() => inflight.delete(hostId))
  inflight.set(hostId, p)
  return p
}

export function resetLeaseTtlCacheForTests(): void {
  cache.clear()
  inflight.clear()
}
