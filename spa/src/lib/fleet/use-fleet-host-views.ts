// spa/src/lib/fleet/use-fleet-host-views.ts — the fleet's hosts as the UI lists them (the sidebar cells, the Hosts page group): the
// snapshot's hosts mapped to the App's own host configs, and the look each is drawn with. Nothing here knows the wire.
import { useMemo } from 'react'
import { useHostStore } from '../../stores/useHostStore'
import { useHostLookResolver, type HostLook } from '../host-look'
import { useFleetStore, fleetHostsView, type FleetHostView } from './store'

/** No fleet from a primary this App can see: the App's own hosts, without readings. */
function configuredViews(hostOrder: string[], hosts: Record<string, unknown>): FleetHostView[] {
  return hostOrder.filter((id) => hosts[id]).map((id) => ({
    id, hostConfigId: id, kind: 'daemon', alias: '', name: id, icon: '', os: '', status: 'ok', error: null, last_ok_at: null, fast: null, slow: null,
  }))
}

export type FleetHostLook = Pick<HostLook, 'colors' | 'icon' | 'iconWeight'>

export function useFleetHostViews() {
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
  const look = (v: FleetHostView): FleetHostLook => {
    if (v.hostConfigId) return lookOf(v.hostConfigId)
    return { colors: v.colors, icon: v.icon, iconWeight: v.icon_weight as HostLook['iconWeight'] }
  }
  return { views, look, snapshot, waitingPrimary, primaryElsewhere }
}
