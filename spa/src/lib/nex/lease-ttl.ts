// spa/src/lib/nex/lease-ttl.ts — the lease TTL a host advertises in
// GET /v1/capabilities, read from `useNexHostStore` (the one per-host
// capabilities cache, spec §4.1) so it follows the store's TTL and
// invalidation instead of a second, page-lifetime cache of its own. The
// renew cadence (ttl/3) and the idle window (2×ttl) both derive from it.
import { useNexHostStore } from '../../stores/useNexHostStore'

export const DEFAULT_LEASE_TTL_S = 120

export async function getLeaseTtlSeconds(hostId: string): Promise<number> {
  await useNexHostStore.getState().ensure(hostId)
  const ttl = useNexHostStore.getState().byHost[hostId]?.capabilities?.lease?.ttl_seconds
  return typeof ttl === 'number' && ttl > 0 ? ttl : DEFAULT_LEASE_TTL_S
}
