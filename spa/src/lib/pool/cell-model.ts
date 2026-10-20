// spa/src/lib/pool/cell-model.ts — what a host cell and its card draw when the resource pool is the source (plan "WWD-7 design",
// "Cells and card"). The cell keeps today's look: a live host is handed to it as a fleet host whose readings are the pool's facts, so
// the ring, the bars and the readings of the card are one code path. What the fleet has no word for (worker, headroom, owner, the
// zone `unknown`) rides along in `pool`. A host the pool does not read goes back to the fleet's readings untouched (fallback) or to
// offline; without a pool view at all the fleet host is returned as it is.
import type { FleetHostView } from '../fleet/store'
import type { FleetFast } from '../fleet/types'
import type { FallbackReason, PoolHostView } from './host-view'
import type { HeadroomReasons, PoolFacts, PoolHost, PoolLoad, PoolOwner, PoolZone, Slots } from './types'

export interface PoolCellExtra {
  state: 'live' | 'fallback' | 'offline'
  /** The pool's zone word: `unknown` is its own (the fleet's zones have no such). Fallback and offline keep the fleet's. */
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
  /** Fallback: when the pool last read the host (ms). */
  since: number | null
  /** Fallback: why the pool does not read it (wwd unreachable, or answering with old facts); null otherwise. */
  reason: FallbackReason | null
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

export function poolCellModel(fleet: FleetHostView, view: PoolHostView | null, pool: PoolHost | null): PoolCellModel {
  if (!view) return { view: fleet, pool: null }
  const none = { load: null, worker: null, headroom: null, reasons: null, sessionsRoom: null, owner: null, psi: null, zone: null, reason: null }
  if (view.state === 'live') {
    const f = view.facts
    return {
      view: { ...fleet, status: 'ok', error: null, last_ok_at: f.at, fast: factsToFast(f, pool?.load?.score ?? fleet.fast?.load ?? 0), slow: null },
      pool: {
        state: 'live', zone: f.derived.zone, load: pool?.load ?? null,
        worker: { enabled: f.worker.enabled, slots: f.worker.slots, running: f.worker.running },
        headroom: f.derived.headroom, reasons: pool?.headroom_reasons ?? null, sessionsRoom: f.derived.sessions_room, owner: f.owner,
        psi: f.mem.psi ? { some10: f.mem.psi.some10, full10: f.mem.psi.full10 } : null, since: null, reason: null,
      },
    }
  }
  if (view.state === 'fallback') return { view: fleet, pool: { ...none, state: 'fallback', since: view.since, reason: view.reason } }
  return { view: { ...fleet, status: 'offline', fast: null, last_ok_at: view.lastOkAt }, pool: { ...none, state: 'offline', since: null } }
}
