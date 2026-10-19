// spa/src/components/fleet/FleetCells.tsx — the fleet cells at the bottom of the sidebar (fleet spec rev 2 §8.1): one cell per
// host, under the BottomNav. The wide bar lays them out like the session list's member beads (TeamMemberBeads: flex-wrap, no
// separators), so they wrap with the sidebar's width; the narrow bar stacks them in a column. No gap between cells in either
// (journal v5 U9: the hosts closer together; the cells' own 3 px sides still part them): at 0, five 44 px cells fit one line
// at the default 240 px width, at 1 px the fifth would wrap. Reads the fleet from `useFleetStore` only; nothing here knows the
// wire.
import { useMemo } from 'react'
import { useFleetStore, fleetHostsView, type FleetHostView } from '../../lib/fleet/store'
import { openWatchArea } from '../../features/workspace/lib/open-watch-area'
import { useHostStore } from '../../stores/useHostStore'
import { useHostLookResolver, type HostLook } from '../../lib/host-look'
import { usePoolCellModels } from '../../lib/pool/use-cell-models'
import { HostCell } from './HostCell'

export interface FleetCellsProps {
  variant: 'wide' | 'narrow'
}

/** No fleet from a primary this App can see: the App's own hosts, without readings. */
function configuredViews(hostOrder: string[], hosts: Record<string, unknown>): FleetHostView[] {
  return hostOrder.filter((id) => hosts[id]).map((id) => ({
    id, hostConfigId: id, kind: 'daemon', alias: '', name: id, icon: '', os: '', status: 'ok', error: null, last_ok_at: null, fast: null, slow: null,
  }))
}

export function FleetCells({ variant }: FleetCellsProps) {
  const snapshot = useFleetStore((s) => s.snapshot)
  const waitingPrimary = useFleetStore((s) => s.waitingPrimary)
  const primaryElsewhere = useFleetStore((s) => s.primaryElsewhere)
  const hosts = useHostStore((s) => s.hosts)
  const hostOrder = useHostStore((s) => s.hostOrder)
  const lookOf = useHostLookResolver()
  const views = useMemo(
    () => (snapshot ? fleetHostsView(snapshot, { hosts, hostOrder }) : primaryElsewhere ? configuredViews(hostOrder, hosts) : []).map((v) => (v.hostConfigId ? { ...v, name: lookOf(v.hostConfigId).name ?? v.name } : v)),
    [snapshot, primaryElsewhere, hosts, hostOrder, lookOf],
  )
  // The resource pool, when a daemon announces it, is the source of the readings; otherwise `models` are the fleet hosts as they were.
  const { models, active: poolActive } = usePoolCellModels(views)
  if (views.length === 0) return null

  const look = (v: FleetHostView): Pick<HostLook, 'colors' | 'icon' | 'iconWeight'> => {
    if (v.hostConfigId) return lookOf(v.hostConfigId)
    return { colors: v.colors, icon: v.icon, iconWeight: v.icon_weight as HostLook['iconWeight'] }
  }
  // Every cell, daemon or probe host, opens the watcher area on that host's tab (watcher area spec §2.2). The fleet's own host
  // id (`v.id`) names the tab, not the App's HostConfig id.
  const open = (v: FleetHostView) => openWatchArea({ host: v.id })
  const wide = variant === 'wide'

  return (
    <div
      data-testid="fleet-cells"
      data-variant={variant}
      className={wide ? 'flex flex-wrap shrink-0 items-center content-start gap-0 px-2 pb-2 pt-1' : 'flex shrink-0 flex-col items-center gap-0 pb-1'}
    >
      {models.map((m) => (
        <HostCell key={m.view.id} view={m.view} pool={m.pool} look={look(m.view)} variant={variant} isActive={false} waitingPrimary={!poolActive && waitingPrimary} primaryElsewhere={!poolActive && primaryElsewhere && !snapshot} onOpen={() => open(m.view)} />
      ))}
    </div>
  )
}
