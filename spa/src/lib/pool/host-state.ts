// spa/src/lib/pool/host-state.ts — the one host state every surface reads (monitoring tabs spec M21, A2, W3). It takes the pool's row only:
// no fleet reading, no fallback. ok: the pool reads the host. wwd_down: the machine is there, its wwd does not answer. host_down: neither
// does. unknown: not checked yet, the check failed, or wwd answers but its facts stopped (`stalled`).
import type { PoolFacts, PoolHost } from './types'

/** Facts older than this are not a live reading. */
export const LIVE_MS = 30_000

export type PoolHostState =
  | { state: 'ok'; hostId: string; alias: string; pool: PoolHost; facts: PoolFacts }
  | {
    state: 'wwd_down' | 'host_down' | 'unknown'
    hostId: string
    alias: string
    pool: PoolHost
    /** When the pool last read facts from this host (ms), kept by the store by `host_id`; null if it never did. */
    lastFactsAt: number | null
    /** When the probe last ran (`liveness_at`, ms); null when the row has none. */
    checkedAt: number | null
    /** Since when the state has held (`liveness_since`, ms); null when the row has none. */
    since: number | null
    /** wwd answered but its facts are missing or older than `LIVE_MS` (W3): the card says "readings stopped at hh:mm:ss". */
    stalled: boolean
  }

/**
 * A peer row (it has `liveness`): the daemon's word stands; `ok` also needs fresh facts. A row without `liveness` (the local host, a daemon
 * that does not send it yet): reachable with fresh facts is ok, anything else unknown.
 */
export function poolHostState(pool: PoolHost, lastFactsAt: number | null, now: number): PoolHostState {
  const { host_id: hostId, alias, facts } = pool
  const fresh = facts !== null && now - facts.at <= LIVE_MS
  const rest = { hostId, alias, pool, lastFactsAt, checkedAt: pool.liveness_at ?? null, since: pool.liveness_since ?? null }
  if (pool.liveness === undefined) {
    if (pool.reachable && fresh) return { state: 'ok', hostId, alias, pool, facts }
    return { state: 'unknown', ...rest, stalled: pool.reachable }
  }
  if (pool.liveness === 'ok') {
    if (fresh) return { state: 'ok', hostId, alias, pool, facts }
    return { state: 'unknown', ...rest, stalled: true }
  }
  return { state: pool.liveness, ...rest, stalled: false }
}

/** Is the host ok right now? (The store's event-mode check, M20.) */
export const hostIsOk = (pool: PoolHost, now: number): boolean => poolHostState(pool, null, now).state === 'ok'
