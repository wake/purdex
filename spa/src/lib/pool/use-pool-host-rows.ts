// spa/src/lib/pool/use-pool-host-rows.ts — the Hosts page's view of the resource pool: one row per pool host (sidebar group, overview
// table, host page), from the same models the sidebar cells use, so a host is in one state everywhere.
import { useFleetHostViews, type FleetHostLook } from '../fleet/use-fleet-host-views'
import { matchPoolHost } from './fleet-bridge'
import { usePoolStore } from './store'
import { usePoolHostDaemons } from './daemons'
import { usePoolCellModels } from './fleet-bridge'
import type { PoolFacts, PoolHost, PoolLiveness, PoolZone, Slots } from './types'

/** What the host page's 「現在」 shows: wwd's reading of an ok host (nothing for a host that is not ok — there is no fleet reading to fall back on). */
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

/** The status light of the pool's state (M21): ok green, wwd_down yellow, host_down and unknown grey. (`error` is retired with the fleet fallback; the type keeps it until MON-12 removes the dot's other users.) */
export type PoolHostDot = 'live' | 'warn' | 'off' | 'error'

export interface PoolHostRow {
  /** The fleet host's id (a probe host's config id, a daemon host's own id). */
  id: string
  name: string
  kind: 'daemon' | 'probe'
  /** The App's HostConfig id for a daemon host. */
  hostConfigId?: string
  look: FleetHostLook
  state: PoolLiveness
  dot: PoolHostDot
  zone: PoolZone | null
  /** Ok only. */
  worker: { enabled: boolean; slots: Slots; running: Slots } | null
  headroom: Slots | null
  /** Ok: what wwd read. Otherwise none. */
  readings: PoolReadings | null
  /** The pool's own record of the host (labels, proto, platform, facts); null while the pool does not list it. */
  host: PoolHost | null
  /** wwd's version from the pool's list; '' while the pool does not list the host. */
  version: string
  /** When the probe last ran (ms); null for an ok host, the local host and a host the pool does not list. */
  checkedAt: number | null
  /** When the pool last read facts from the host (ms); null if it never did. */
  lastFactsAt: number | null
  /** wwd answers but its readings stopped (spec W3). */
  stalled: boolean
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
    const state = pool?.state ?? 'unknown'
    const poolHost = matchPoolHost(hosts, original, daemons)
    const dot: PoolHostDot = state === 'ok' ? 'live' : state === 'wwd_down' ? 'warn' : 'off'
    return {
      id: m.view.id, name: m.view.name, kind: m.view.kind, hostConfigId: m.view.hostConfigId, look: look(original), state, dot,
      zone: pool?.zone ?? null, worker: pool?.worker ?? null, headroom: pool?.headroom ?? null,
      readings: state === 'ok' && poolHost?.facts ? readingsFromFacts(poolHost.facts) : null,
      host: poolHost, version: poolHost?.version ?? '',
      checkedAt: pool?.checkedAt ?? null, lastFactsAt: pool?.lastFactsAt ?? null, stalled: pool?.stalled ?? false,
    }
  })
  return { rows, active, oldDaemon }
}
