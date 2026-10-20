// spa/src/lib/pool/host-looks.ts — a pool host's look and order for the monitoring screens (monitoring tabs spec A3, M9, M13, W7).
// A pool host is keyed by the wwd `host_id`. Its colour and icon live where the rest of the App already reads them: a host with a daemon in the Purdex host's look
// (`useHostLookStore`, through `lib/host-look.ts` — the tab badge shows the same one); a host without one, and every indicator, in `usePoolHostsStore`.
import { useMemo } from 'react'
import { lookKeyOf, useHostStore } from '../../stores/useHostStore'
import { useHostLookStore, type HostLookEntry } from '../../stores/useHostLookStore'
import { usePoolHostsStore, type PoolHostIndicator, type PoolHostLook } from '../../stores/usePoolHostsStore'
import { useUISettingsStore, type FleetCellIndicator } from '../../stores/useUISettingsStore'
import { useHostLookResolver, type HostLook } from '../host-look'
import { readPoolHostDaemons, usePoolHostDaemons } from './daemons'

/** A pool host's look as the screens draw it: colour and icon from their home, the indicator resolved (stored or default). */
export interface PoolHostResolvedLook {
  colors?: HostLook['colors']
  color?: string
  icon?: string
  iconWeight?: HostLook['iconWeight']
  indicator: PoolHostIndicator
}

/** The edits `writePoolHostLook` takes; `null` clears a field. */
export interface PoolHostLookPatch {
  colors?: HostLook['colors'] | null
  icon?: string | null
  iconWeight?: HostLook['iconWeight'] | null
  indicator?: PoolHostIndicator
}

/** The shape and data of the old global 「主機格指示」, which is each host's default until it is set (W7). */
export function defaultIndicator(global: FleetCellIndicator): PoolHostIndicator {
  if (global === 'bars-pressure') return { shape: 'bars', data: ['cpu', 'pressure'] }
  if (global === 'bars-memory') return { shape: 'bars', data: ['cpu', 'mem'] }
  return { shape: 'ring', data: ['load'] }
}

export function indicatorOf(look: Pick<PoolHostLook, 'indicator'> | undefined, global: FleetCellIndicator): PoolHostIndicator {
  return look?.indicator ?? defaultIndicator(global)
}

/** M13: the ids in `order` first (those the pool lists), the others after them in the pool's own order. */
export function orderPoolHosts<T extends { host_id: string }>(hosts: readonly T[], order: readonly string[]): T[] {
  const byId = new Map(hosts.map((h) => [h.host_id, h]))
  const out: T[] = []
  for (const id of order) {
    const h = byId.get(id)
    if (h) { out.push(h); byId.delete(id) }
  }
  for (const h of hosts) if (byId.has(h.host_id)) out.push(h)
  return out
}

function compose(base: HostLook | PoolHostLook | undefined, indicator: PoolHostIndicator): PoolHostResolvedLook {
  const out: PoolHostResolvedLook = { indicator }
  if (base?.colors !== undefined) out.colors = base.colors
  if (base?.color !== undefined) out.color = base.color
  if (base?.icon !== undefined) out.icon = base.icon
  if (base?.iconWeight !== undefined) out.iconWeight = base.iconWeight
  return out
}

export function usePoolHostLook(hostId: string): PoolHostResolvedLook {
  const daemon = usePoolHostDaemons()[hostId]
  const resolve = useHostLookResolver()
  const stored = usePoolHostsStore((s) => (Object.hasOwn(s.looks, hostId) ? s.looks[hostId] : undefined))
  const global = useUISettingsStore((s) => s.fleetCellIndicator)
  return useMemo(
    () => compose(daemon !== undefined ? resolve(daemon) : stored, indicatorOf(stored, global)),
    [daemon, resolve, stored, global],
  )
}

function applyField<K extends keyof HostLookEntry>(next: Record<string, unknown>, key: K, value: HostLookEntry[K] | null | undefined) {
  if (value === undefined) return
  if (value === null) delete next[key]
  else next[key] = value
}

/**
 * Writes an edit where the host's look lives: colour and icon of a host with a daemon into the Purdex host's look (under the key `lookKeyOf` gives, as the Hosts page writes it),
 * those of a host without one and every indicator into the pool hosts store. An id that is not a wwd `host_id` writes nothing (the store refuses it).
 */
export function writePoolHostLook(hostId: string, patch: PoolHostLookPatch): void {
  const daemon = readPoolHostDaemons()[hostId]
  const { indicator, ...face } = patch
  if (indicator !== undefined) usePoolHostsStore.getState().patchLook(hostId, (cur) => ({ ...cur, indicator }))
  if (Object.keys(face).length === 0) return
  if (daemon !== undefined) {
    const host = useHostStore.getState().hosts[daemon]
    if (!host) return
    const key = lookKeyOf(host, useHostLookStore.getState().looks)
    useHostLookStore.getState().patchLook(key, (cur) => editFace(cur, face))
    return
  }
  usePoolHostsStore.getState().patchLook(hostId, (cur) => editFace(cur, face))
}

function editFace<T extends HostLookEntry | PoolHostLook>(cur: T | undefined, face: Omit<PoolHostLookPatch, 'indicator'>): T {
  const next: Record<string, unknown> = { ...cur }
  applyField(next, 'colors', face.colors)
  applyField(next, 'icon', face.icon)
  applyField(next, 'iconWeight', face.iconWeight)
  return next as T
}
