// spa/src/lib/pool/daemons.ts — which App host is the daemon of which pool host. A pool host is keyed by the wwd `host_id`; a daemon says which wwd it fronts in `/api/info.wwd`
// (cached by the Nex host store). The wwd id is never the Purdex daemon's own host id, so the two are only ever joined through this map.
import { useMemo } from 'react'
import { useHostStore, type HostRuntime } from '../../stores/useHostStore'
import { useNexHostStore, type NexHostEntry } from '../../stores/useNexHostStore'

export const POOL_CAPABILITY = 'pool.v1'

interface Inputs {
  hostOrder: string[]
  runtime: Record<string, Pick<HostRuntime, 'status'> | undefined>
  byHost: Record<string, NexHostEntry | undefined>
}

/**
 * wwd `host_id` → App host id of the daemon in front of it. Only connected daemons whose entry was read (`fetchedAt` 0 marks stale data from an earlier connection) count;
 * when two claim one id the first in host order wins.
 */
export function poolHostDaemons({ hostOrder, runtime, byHost }: Inputs): Record<string, string> {
  const out: Record<string, string> = {}
  for (const id of hostOrder) {
    const e = byHost[id]
    if (runtime[id]?.status !== 'connected' || !e || e.fetchedAt <= 0 || !e.wwd) continue
    if (!Object.hasOwn(out, e.wwd.hostId)) out[e.wwd.hostId] = id
  }
  return out
}

export function readPoolHostDaemons(): Record<string, string> {
  const { hostOrder, runtime } = useHostStore.getState()
  return poolHostDaemons({ hostOrder, runtime, byHost: useNexHostStore.getState().byHost })
}

export function usePoolHostDaemons(): Record<string, string> {
  const hostOrder = useHostStore((s) => s.hostOrder)
  const runtime = useHostStore((s) => s.runtime)
  const byHost = useNexHostStore((s) => s.byHost)
  return useMemo(() => poolHostDaemons({ hostOrder, runtime, byHost }), [hostOrder, runtime, byHost])
}

/**
 * A daemon can learn its wwd after the App first read `/api/info`, or be pointed at another one: ask again every connected daemon that announces `pool.v1` or whose `/api/info` never answered (no capabilities in its entry).
 * `ensure` is held to the cache TTL (60 s), so this costs at most one read a minute each.
 */
export function refreshPoolIdentities(): void {
  const { hostOrder, runtime } = useHostStore.getState()
  const { byHost, ensure } = useNexHostStore.getState()
  for (const id of hostOrder) {
    const e = byHost[id]
    if (runtime[id]?.status === 'connected' && e && (!e.daemonCapabilities || e.daemonCapabilities.includes(POOL_CAPABILITY))) void ensure(id)
  }
}
