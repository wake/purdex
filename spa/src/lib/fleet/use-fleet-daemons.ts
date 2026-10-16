// spa/src/lib/fleet/use-fleet-daemons.ts — which App hosts are fleet daemons. `known` carry `fleet.v1`; `reachable` are the ones of
// them that are online. The probe list is written to every known daemon, so writing is offered only while all are reachable.
import { useMemo } from 'react'
import { useHostStore } from '../../stores/useHostStore'
import { useFleetStore } from './store'

export function useFleetDaemons(): { known: string[]; reachable: string[] } {
  const hostOrder = useHostStore((s) => s.hostOrder)
  const online = useHostStore((s) => s.runtime)
  const byHost = useFleetStore((s) => s.byHost)
  return useMemo(() => {
    const known = hostOrder.filter((id) => byHost[id]?.support === 'yes')
    return { known, reachable: known.filter((id) => online[id]?.status === 'connected') }
  }, [hostOrder, online, byHost])
}
