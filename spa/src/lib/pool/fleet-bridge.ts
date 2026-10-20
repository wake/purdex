// spa/src/lib/pool/fleet-bridge.ts — (moved from cell-model.ts, with usePoolCellModels from use-cell-models.ts; the transitional reader of fleet views for the host page and the Hosts page, MON-1c-0) what a host cell and its card draw when the resource pool is the source (plan "WWD-7 design",
// "Cells and card"). The cell keeps today's look: an ok host is handed to it as a fleet host whose readings are the pool's facts, so
// the ring, the bars and the readings of the card are one code path. What the fleet has no word for (worker, headroom, owner, the
// zone `unknown`) rides along in `pool`. A host that is not ok reads as offline with no readings (MON-1d: the fleet is no fallback any
// more); a host the pool does not list is `unknown`; before the pool has said anything the fleet host is returned as it is.
import { useMemo } from 'react'
import type { FleetHostView } from '../fleet/store'
import type { FleetFast } from '../fleet/types'
import { poolHostState, type PoolHostState } from './host-state'
import { usePoolStore } from './store'
import { useClock, useRunPool } from './use-cell-models'
import { usePoolHostDaemons } from './daemons'
import type { HeadroomReasons, PoolFacts, PoolHost, PoolLiveness, PoolLoad, PoolOwner, PoolZone, Slots } from './types'

export interface PoolCellExtra {
  state: PoolLiveness
  /** The pool's zone word: `unknown` is its own (the fleet's zones have no such). Null while the host is not ok. */
  zone: PoolZone | null
  /** The daemon's load score and what drives it; null while it has no `pool.assess.v1` (the ring then keeps the fleet's score). */
  load: PoolLoad | null
  worker: { enabled: boolean; slots: Slots; running: Slots } | null
  headroom: Slots | null
  /** Why each class is at 0, in spec order; null while the daemon does not send them (0 is then shown without a reason). */
  reasons: HeadroomReasons | null
  sessionsRoom: number | null
  owner: PoolOwner | null
  /** Linux only. */
  psi: { some10: number; full10: number } | null
  /** When the probe last ran (`liveness_at`, ms); null for an ok host, the local host and a host the pool does not list. */
  checkedAt: number | null
  /** When the pool last read facts from the host (ms); null if it never did. */
  lastFactsAt: number | null
  /** wwd answers but its facts stopped (spec W3). */
  stalled: boolean
}

export interface PoolCellModel {
  view: FleetHostView
  pool: PoolCellExtra | null
}

/**
 * The pool host for a fleet host: a daemon host by the pool host its App host is the daemon of (first), else by alias (`daemons`: wwd `host_id` → App host id, see `daemons.ts`).
 * An empty alias matches nothing by alias.
 */
export function matchPoolHost(hosts: PoolHost[], v: Pick<FleetHostView, 'alias' | 'kind' | 'hostConfigId'>, daemons: Record<string, string>): PoolHost | null {
  const byDaemon = v.kind === 'daemon' && v.hostConfigId ? hosts.find((h) => daemons[h.host_id] === v.hostConfigId) : undefined
  return byDaemon ?? hosts.find((h) => v.alias !== '' && h.alias === v.alias) ?? null
}

/** The facts as the fleet's fast reading, which the cell and the card already draw. `zone` is a placeholder for `unknown`. */
export function factsToFast(f: PoolFacts, load: number): FleetFast {
  const fullest = f.disk.reduce<PoolFacts['disk'][number] | null>((best, d) => (best === null || d.used_pct > best.used_pct ? d : best), null)
  return {
    at: f.at,
    cpu: 100 - f.cpu.idle_pct,
    mem: f.mem.total_mb > 0 ? ((f.mem.total_mb - f.mem.available_mb) / f.mem.total_mb) * 100 : 0,
    level: f.mem.pressure,
    page_rate: f.mem.page_out_rate ?? 0,
    disk: fullest?.used_pct ?? 0,
    disk_free_gb: fullest?.free_gb ?? 0,
    load1: f.cpu.load1,
    ncpu: f.cpu.ncpu,
    zone: f.derived.zone === 'unknown' ? 'normal' : f.derived.zone,
    load,
  }
}

/** A host the pool does not list: `unknown`, nothing read. `lastFactsAt` is what the store kept for it, if anything. */
export interface UnlistedState { state: 'unknown'; lastFactsAt: number | null }
export const unlistedState = (lastFactsAt: number | null = null): UnlistedState => ({ state: 'unknown', lastFactsAt })

export function poolCellModel(fleet: FleetHostView, st: PoolHostState | UnlistedState | null, pool: PoolHost | null): PoolCellModel {
  if (!st) return { view: fleet, pool: null }
  if (st.state === 'ok' && 'facts' in st) {
    const f = st.facts
    return {
      view: { ...fleet, status: 'ok', error: null, last_ok_at: f.at, fast: factsToFast(f, pool?.load?.score ?? fleet.fast?.load ?? 0), slow: null },
      pool: {
        state: 'ok', zone: f.derived.zone, load: pool?.load ?? null,
        worker: { enabled: f.worker.enabled, slots: f.worker.slots, running: f.worker.running },
        headroom: f.derived.headroom, reasons: pool?.headroom_reasons ?? null, sessionsRoom: f.derived.sessions_room, owner: f.owner,
        psi: f.mem.psi ? { some10: f.mem.psi.some10, full10: f.mem.psi.full10 } : null, checkedAt: null, lastFactsAt: f.at, stalled: false,
      },
    }
  }
  const none = { load: null, worker: null, headroom: null, reasons: null, sessionsRoom: null, owner: null, psi: null, zone: null }
  const rest = 'pool' in st ? { checkedAt: st.checkedAt, stalled: st.stalled } : { checkedAt: null, stalled: false }
  return {
    view: { ...fleet, status: 'offline', fast: null, last_ok_at: st.lastFactsAt },
    pool: { ...none, state: st.state, lastFactsAt: st.lastFactsAt, ...rest },
  }
}

export function usePoolCellModels(views: FleetHostView[]): { models: PoolCellModel[]; active: boolean } {
  useRunPool()
  const now = useClock()
  const hosts = usePoolStore((s) => s.hosts)
  const oldDaemon = usePoolStore((s) => s.oldDaemon)
  const answered = usePoolStore((s) => s.sourceHostId !== null)
  const failed = usePoolStore((s) => s.error !== null)
  const lastFactsAt = usePoolStore((s) => s.lastFactsAt)
  const daemons = usePoolHostDaemons()
  // Active once the pool has said something: a list (an empty one is an answer too), or that every daemon failed. Before the first read the cells stay the fleet's.
  const active = !oldDaemon && (answered || failed)
  const models = useMemo(() => views.map((v) => {
    if (!active) return poolCellModel(v, null, null)
    const pool = matchPoolHost(hosts, v, daemons)
    return poolCellModel(v, pool ? poolHostState(pool, lastFactsAt[pool.host_id] ?? null, now) : unlistedState(), pool)
  }), [views, active, hosts, daemons, lastFactsAt, now])
  return { models, active }
}
