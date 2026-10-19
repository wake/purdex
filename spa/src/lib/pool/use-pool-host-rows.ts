// spa/src/lib/pool/use-pool-host-rows.ts — the Hosts page's view of the resource pool: one row per pool host (sidebar group, overview
// table, host page), from the same models the sidebar cells use, so a host is in one state everywhere.
import { useFleetHostViews, type FleetHostLook } from '../fleet/use-fleet-host-views'
import { matchPoolHost } from './cell-model'
import { usePoolStore } from './store'
import { usePoolCellModels } from './use-cell-models'
import type { PoolZone, Slots } from './types'

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
  /** wwd's version from the pool's list; '' while the pool does not list the host. */
  version: string
  /** Fallback: when the pool last read the host (ms). Offline: the newest last-ok of the pool and the fleet. */
  since: number | null
}

export function usePoolHostRows(): { rows: PoolHostRow[]; active: boolean; oldDaemon: boolean } {
  const { views, look } = useFleetHostViews()
  const { models, active } = usePoolCellModels(views)
  const hosts = usePoolStore((s) => s.hosts)
  const oldDaemon = usePoolStore((s) => s.oldDaemon)
  const rows = models.map((m, i): PoolHostRow => {
    const original = views[i]
    const pool = m.pool
    const state = pool?.state ?? 'fallback'
    const dot: PoolHostDot = state === 'live' ? 'live' : state === 'fallback' ? 'warn' : original.status === 'error' ? 'error' : 'off'
    return {
      id: m.view.id, name: m.view.name, kind: m.view.kind, hostConfigId: m.view.hostConfigId, look: look(original), state, dot,
      zone: pool?.zone ?? null, worker: pool?.worker ?? null, headroom: pool?.headroom ?? null,
      version: matchPoolHost(hosts, original)?.version ?? '',
      since: state === 'offline' ? m.view.last_ok_at : pool?.since ?? null,
    }
  })
  return { rows, active, oldDaemon }
}
