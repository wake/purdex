// spa/src/lib/pool/cell-model.ts — what a sidebar host cell and its card draw (monitoring tabs spec M8–M12, M21). The pool is the only source: the model is built from a `MonitorHost`
// (one pool row, its state, its look) and nothing here reads the fleet. The readings of a host in state `ok` are its facts in `CellFast`'s shape (the ring, the bars and the card rows are
// one code path); a host in any other state has no readings, only the state.
import type { MonitorHost } from './use-monitor-hosts'
import type { PoolHostState } from './host-state'
import type { PoolHostResolvedLook } from './host-looks'
import type { HeadroomReasons, PoolFacts, PoolLoad, PoolOwner, PoolZone, Slots } from './types'

/** The facts as the cell and the card read them (the shape the fleet's fast reading had). */
export interface CellFast {
  at: number
  cpu: number
  mem: number
  level: number
  page_rate: number
  disk: number
  disk_free_gb: number
  load1: number
  ncpu: number
  zone: 'normal' | 'warning' | 'danger'
  /** The composite score, 0–100. */
  load: number
}

export interface CellView {
  hostId: string
  /** What the host is called on the cell and the card: its alias. */
  name: string
  state: PoolHostState
  look: PoolHostResolvedLook
  /** Set while the host is only the kept one because the whole pool cannot be read (M25). */
  poolFailure: MonitorHost['poolFailure']
  /** The number at the corner of the cell and its colour; null: none to show. */
  count: MonitorHost['count']
  /** The readings; null unless the state is `ok`. */
  fast: CellFast | null
  /** The pool's zone word (`unknown` is its own); null unless `ok`. */
  zone: PoolZone | null
  /** The daemon's load score and what drives it; null while it has no `pool.assess.v1`. */
  load: PoolLoad | null
  worker: { enabled: boolean; slots: Slots; running: Slots } | null
  headroom: Slots | null
  /** Why each class is at 0; null while the daemon does not send them. */
  reasons: HeadroomReasons | null
  sessionsRoom: number | null
  owner: PoolOwner | null
  /** Linux only. */
  psi: { some10: number; full10: number } | null
  /** What the card's lower rows say beyond the pool's own (M11); null unless `ok`. `checkedAt` is the daemon's last probe of this peer (ms), null for the local host or a daemon that sends none. `local` is `MonitorHost.local`. */
  detail: { version: string; memAvailGb: number; swapUsedGb: number | null; local: boolean; checkedAt: number | null } | null
}

/** The facts as `CellFast`. `zone` is a placeholder (`normal`) for the pool's `unknown`, which rides in `CellView.zone`. */
export function factsToCellFast(f: PoolFacts, load: number): CellFast {
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

export function cellOf(m: MonitorHost): CellView {
  const base = { hostId: m.hostId, name: m.alias, state: m.state, look: m.look, poolFailure: m.poolFailure, count: m.count }
  const none = { fast: null, zone: null, load: null, worker: null, headroom: null, reasons: null, sessionsRoom: null, owner: null, psi: null, detail: null }
  if (m.state.state !== 'ok') return { ...base, ...none }
  const f = m.state.facts
  const pool = m.state.pool
  return {
    ...base,
    fast: factsToCellFast(f, pool.load?.score ?? 0),
    zone: f.derived.zone,
    load: pool.load ?? null,
    worker: { enabled: f.worker.enabled, slots: f.worker.slots, running: f.worker.running },
    headroom: f.derived.headroom,
    reasons: pool.headroom_reasons ?? null,
    sessionsRoom: f.derived.sessions_room,
    owner: f.owner,
    psi: f.mem.psi ? { some10: f.mem.psi.some10, full10: f.mem.psi.full10 } : null,
    detail: {
      version: m.state.pool.version,
      memAvailGb: f.mem.available_mb / 1024,
      swapUsedGb: f.mem.swap_used_mb === null ? null : f.mem.swap_used_mb / 1024,
      local: m.local,
      checkedAt: m.state.pool.liveness_at ?? null,
    },
  }
}
