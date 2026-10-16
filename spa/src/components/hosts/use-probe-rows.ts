// spa/src/components/hosts/use-probe-rows.ts — the probe hosts as rows (sidebar group, overview list, entry header): the fleet
// snapshot says what is observed (status, readings); the daemons' config says what is stored (name, look); a draft that is
// not saved yet shows too.
import { useMemo } from 'react'
import { useFleetStore } from '../../lib/fleet/store'
import { mergedProbeEntries, useFleetConfigStore } from '../../lib/fleet/config-store'
import { useFleetDraftStore } from '../../lib/fleet/config-draft'
import type { HostConfig } from '../../stores/useHostStore'
import type { FleetHost } from '../../lib/fleet/types'
import { useI18nStore } from '../../stores/useI18nStore'

export type ProbeStatus = 'ok' | 'offline' | 'error' | 'unknown'
export interface ProbeRowView {
  id: string
  name: string
  icon: string
  iconWeight: string
  colors?: HostConfig['colors']
  status: ProbeStatus
  /** The snapshot's row, when the primary has one. */
  host: FleetHost | null
  /** Only a draft so far. */
  isNew: boolean
}

export function useProbeRows(): ProbeRowView[] {
  const snapshot = useFleetStore((s) => s.snapshot)
  const cfg = useFleetConfigStore((s) => s.byHost)
  const session = useFleetDraftStore((s) => s.probe)
  const newName = useI18nStore((s) => s.t)('fleet_hosts.entry.new_name')
  return useMemo(() => {
    const out = new Map<string, ProbeRowView>()
    const snapHosts = snapshot ? snapshot.order.map((id) => snapshot.hosts[id]).filter((h) => h.kind === 'probe') : []
    for (const h of snapHosts) {
      out.set(h.id, { id: h.id, name: h.name, icon: h.icon, iconWeight: h.icon_weight ?? '', colors: h.colors, status: h.status, host: h, isNew: false })
    }
    const ready = Object.keys(cfg).filter((id) => cfg[id].status === 'ready')
    for (const e of mergedProbeEntries(ready, cfg)) {
      const have = out.get(e.id)
      out.set(e.id, { id: e.id, name: e.name, icon: e.icon, iconWeight: e.icon_weight, colors: e.colors, status: have?.status ?? 'unknown', host: have?.host ?? null, isNew: false })
    }
    if (session?.isNew && !out.has(session.initial.id)) {
      out.set(session.initial.id, { id: session.initial.id, name: session.draft.name || newName, icon: session.draft.icon, iconWeight: session.draft.icon_weight, colors: session.draft.colors, status: 'unknown', host: null, isNew: true })
    }
    return [...out.values()]
  }, [snapshot, cfg, session, newName])
}
