// spa/src/lib/pool/host-view.ts — the one host state every surface reads (plan "WWD-7 design", host-state table).
// live: the pool reads the host. fallback: the pool does not, the fleet does (never dimmed). offline: neither does.
// "Old daemon" (no daemon announces pool.v1) is not a per-host state: the store says it, see `usePoolStore.oldDaemon`.
import type { FleetHost } from '../fleet/types'
import { LIVE_MS } from './host-state'
import type { PoolFacts, PoolHost } from './types'

// `LIVE_MS` moved to host-state.ts (MON-1b); re-exported until this file goes (MON-1c).
export { LIVE_MS }

/** Why the pool does not read a host that the fleet still does: wwd (or the link to it) does not answer, or it answers but its facts stopped. */
export type FallbackReason = 'unreachable' | 'stale'

export interface PoolHostViewInput {
  alias: string
  /** The host as the pool lists it, or null when the pool does not (wwd down, or it never listed this host). */
  pool: PoolHost | null
  /** The matching fleet host (by alias; a daemon host also by id), or null. */
  fleet: FleetHost | null
  /** The fleet snapshot is stale (`useFleetStore` `stale`): its last values are kept, but are not a reading. */
  fleetStale: boolean
  /** When the pool last read this host (ms), kept by the store; null if it never did. */
  poolLastOk: number | null
}

export type PoolHostView =
  | { state: 'live'; alias: string; pool: PoolHost; facts: PoolFacts }
  | { state: 'fallback'; alias: string; fleet: FleetHost; since: number | null; reason: FallbackReason }
  | { state: 'offline'; alias: string; lastOkAt: number | null }

export function poolHostView(input: PoolHostViewInput, now: number): PoolHostView {
  const { alias, pool, fleet, fleetStale, poolLastOk } = input
  if (pool?.reachable && pool.facts && now - pool.facts.at <= LIVE_MS) return { state: 'live', alias, pool, facts: pool.facts }
  // a peer that stopped answering keeps its last facts, so `reachable` decides before the age of the facts does
  const reason: FallbackReason = pool?.reachable ? 'stale' : 'unreachable'
  if (fleet && fleet.status === 'ok' && fleet.fast && !fleetStale) return { state: 'fallback', alias, fleet, since: poolLastOk, reason }
  const lastOk = [poolLastOk, fleet?.last_ok_at ?? null].filter((t): t is number => t !== null)
  return { state: 'offline', alias, lastOkAt: lastOk.length ? Math.max(...lastOk) : null }
}
