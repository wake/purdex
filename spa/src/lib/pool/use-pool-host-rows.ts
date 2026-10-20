// spa/src/lib/pool/use-pool-host-rows.ts — the Hosts page's view of the resource pool: one row per pool host (sidebar group, overview
// table, host page), from the same models the sidebar cells use, so a host is in one state everywhere.
import { useFleetHostViews, type FleetHostLook } from '../fleet/use-fleet-host-views'
import { matchPoolHost } from './cell-model'
import { usePoolStore } from './store'
import { usePoolHostDaemons } from './daemons'
import type { FallbackReason } from './host-view'
import { usePoolCellModels } from './use-cell-models'
import type { PoolFacts, PoolHost, PoolZone, Slots } from './types'
import type { FleetFast } from '../fleet/types'

/** What the host page's 「現在」 shows, from whichever source the row is read from (wwd live, the fleet's SSH reading in fallback). */
export interface PoolReadings { cpu: number; mem: number | null; level: 1 | 2 | 4; disk: number | null; diskFree: number | null }

const levelOf = (n: number): 1 | 2 | 4 => (n >= 4 ? 4 : n >= 2 ? 2 : 1)

export function readingsFromFacts(f: PoolFacts): PoolReadings {
  const disk = f.disk.reduce<PoolFacts['disk'][number] | null>((m, d) => (m === null || d.used_pct > m.used_pct ? d : m), null)
  return {
    cpu: Math.round(100 - f.cpu.idle_pct),
    mem: f.mem.total_mb > 0 ? Math.round(((f.mem.total_mb - f.mem.available_mb) / f.mem.total_mb) * 100) : null,
    level: levelOf(f.mem.pressure),
    disk: disk ? Math.round(disk.used_pct) : null,
    diskFree: disk ? Math.round(disk.free_gb) : null,
  }
}

export const readingsFromFleet = (f: FleetFast): PoolReadings => ({ cpu: Math.round(f.cpu), mem: Math.round(f.mem), level: levelOf(f.level), disk: Math.round(f.disk), diskFree: Math.round(f.disk_free_gb) })

/** The status light: live green, wwd silent but read over SSH yellow, nobody reads it grey, a fleet error red. */
export type PoolHostDot = 'live' | 'warn' | 'off' | 'error'

export interface PoolHostRow {
  /** The fleet host's id (a probe host's config id, a daemon host's own id). */
  id: string
  name: string
  kind: 'daemon' | 'probe'
  /** The App's HostConfig id for a daemon host. */
  hostConfigId?: string
  look: FleetHostLook
  state: 'live' | 'fallback' | 'offline'
  dot: PoolHostDot
  zone: PoolZone | null
  /** Live only. */
  worker: { enabled: boolean; slots: Slots; running: Slots } | null
  headroom: Slots | null
  /** Live: what wwd read. Fallback: the fleet's reading (wwd's last facts are old by then). Offline: none. */
  readings: PoolReadings | null
  /** The pool's own record of the host (labels, proto, platform, facts); null while the pool does not list it. */
  host: PoolHost | null
  /** wwd's version from the pool's list; '' while the pool does not list the host. */
  version: string
  /** Fallback: when the pool last read the host (ms). Offline: the newest last-ok of the pool and the fleet. */
  since: number | null
  /** Fallback: why the pool does not read the host. */
  reason: FallbackReason | null
}

export function usePoolHostRows(): { rows: PoolHostRow[]; active: boolean; oldDaemon: boolean } {
  const { views, look } = useFleetHostViews()
  const { models, active } = usePoolCellModels(views)
  const hosts = usePoolStore((s) => s.hosts)
  const daemons = usePoolHostDaemons()
  const oldDaemon = usePoolStore((s) => s.oldDaemon)
  const rows = models.map((m, i): PoolHostRow => {
    const original = views[i]
    const pool = m.pool
    const state = pool?.state ?? 'fallback'
    const poolHost = matchPoolHost(hosts, original, daemons)
    const dot: PoolHostDot = state === 'live' ? 'live' : state === 'fallback' ? 'warn' : original.status === 'error' ? 'error' : 'off'
    return {
      id: m.view.id, name: m.view.name, kind: m.view.kind, hostConfigId: m.view.hostConfigId, look: look(original), state, dot,
      zone: pool?.zone ?? null, worker: pool?.worker ?? null, headroom: pool?.headroom ?? null,
      readings: state === 'live' ? (poolHost?.facts ? readingsFromFacts(poolHost.facts) : null) : state === 'fallback' && original.fast ? readingsFromFleet(original.fast) : null,
      host: poolHost, version: poolHost?.version ?? '',
      since: state === 'offline' ? m.view.last_ok_at : pool?.since ?? null, reason: pool?.reason ?? null,
    }
  })
  return { rows, active, oldDaemon }
}
