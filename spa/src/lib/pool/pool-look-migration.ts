// spa/src/lib/pool/pool-look-migration.ts — the one-time copy of the fleet's probe-host looks and order into `usePoolHostsStore` (monitoring tabs spec A3, W2).
// The hosts without a daemon have their look only on the fleet wire (`colors`, `icon`, `icon_weight`); the new store is keyed by the wwd `host_id`. The copy runs before Profile Sync starts
// (`main.tsx`), so the first `settings` build already sees it. Delete with the fleet (WWD-9, MON-12): this is the one exemption of the sidebar's no-fleet-reads guard.
//
// It waits for two things: the pool's first answer and a fresh fleet snapshot. Both there: copy once, write the marker. Not within the timeout: let Profile Sync go, write nothing (also not when a
// snapshot arrives later in this run), count a try; the third try in a row gives up. The marker (`{ done, tries }`) is device-local — never projected, never registered with syncManager.
import { useFleetStore, type FleetSnapshot } from '../fleet/store'
import { usePoolHostsStore, type PoolHostLook } from '../../stores/usePoolHostsStore'
import { STORAGE_KEYS } from '../storage'
import { usePoolStore } from './store'
import { retainPool } from './use-cell-models'
import type { PoolHost } from './types'

const WAIT_MS = 10_000
const MAX_TRIES = 3

interface Marker { done: boolean; tries: number }

function readMarker(): Marker {
  try {
    const raw = localStorage.getItem(STORAGE_KEYS.POOL_HOST_LOOKS_MIGRATED)
    if (raw === null) return { done: false, tries: 0 }
    const v: unknown = JSON.parse(raw)
    if (typeof v === 'object' && v !== null) {
      const { done, tries } = v as { done?: unknown; tries?: unknown }
      return { done: done === true, tries: typeof tries === 'number' && tries >= 0 ? Math.floor(tries) : 0 }
    }
  } catch {
    // a broken marker reads as "not migrated": the copy only ever adds what is absent, so a rerun is harmless
  }
  return { done: false, tries: 0 }
}

function writeMarker(m: Marker): void {
  try {
    localStorage.setItem(STORAGE_KEYS.POOL_HOST_LOOKS_MIGRATED, JSON.stringify(m))
  } catch {
    // a failed write only means the next boot asks again
  }
}

/** Probe hosts' looks keyed by pool `host_id`, and the fleet's order as pool `host_id`s (a host the pool does not list is skipped). */
function plan(poolHosts: readonly PoolHost[], snapshot: FleetSnapshot): { looks: Record<string, PoolHostLook>; order: string[] } {
  const idOfAlias = new Map(poolHosts.filter((h) => h.alias !== '').map((h) => [h.alias, h.host_id]))
  const looks: Record<string, PoolHostLook> = {}
  const order: string[] = []
  for (const fleetId of snapshot.order) {
    const h = Object.hasOwn(snapshot.hosts, fleetId) ? snapshot.hosts[fleetId] : undefined
    const hostId = h ? idOfAlias.get(h.alias) : undefined
    if (!h || hostId === undefined) continue
    if (!order.includes(hostId)) order.push(hostId)
    if (h.kind !== 'probe') continue
    const look: PoolHostLook = {}
    if (h.colors !== undefined) look.colors = h.colors
    if (h.icon !== '') look.icon = h.icon
    if (h.icon_weight !== undefined) look.iconWeight = h.icon_weight as PoolHostLook['iconWeight']
    if (Object.keys(look).length > 0 && !Object.hasOwn(looks, hostId)) looks[hostId] = look
  }
  return { looks, order }
}

function copy(poolHosts: readonly PoolHost[], snapshot: FleetSnapshot): void {
  const { looks, order } = plan(poolHosts, snapshot)
  const store = usePoolHostsStore.getState()
  store.putLooksIfAbsent(looks)
  if (store.order.length === 0 && order.length > 0) store.setOrder(order)
}

/** Resolves when Profile Sync may start. Never rejects. */
export function bootPoolLooks(waitMs: number = WAIT_MS): Promise<void> {
  return new Promise<void>((resolve) => {
    try {
      const marker = readMarker()
      if (marker.done) { resolve(); return }
      const release = retainPool()
      let finished = false
      let unsubscribe: Array<() => void> = []
      const finish = () => {
        if (finished) return
        finished = true
        for (const u of unsubscribe) u()
        unsubscribe = []
        clearTimeout(timer)
        release()
        resolve()
      }
      const check = () => {
        if (finished) return
        try {
          const pool = usePoolStore.getState()
          const fleet = useFleetStore.getState()
          if (pool.sourceHostId === null || fleet.snapshot === null || fleet.stale) return
          copy(pool.hosts, fleet.snapshot)
          writeMarker({ done: true, tries: marker.tries })
        } catch {
          return
        }
        finish()
      }
      const timer = setTimeout(() => {
        if (finished) return
        const tries = marker.tries + 1
        writeMarker({ done: tries >= MAX_TRIES, tries })
        finish()
      }, waitMs)
      unsubscribe = [usePoolStore.subscribe(check), useFleetStore.subscribe(check)]
      check()
    } catch {
      resolve()
    }
  })
}
