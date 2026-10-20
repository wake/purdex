// spa/src/lib/pool/host-looks.ts — a pool host's look and order for the monitoring screens (monitoring tabs spec A3, M9, M13, W7).
// A pool host is keyed by the wwd `host_id`. Its colour and icon live where the rest of the App already reads them: a host with a daemon in the Purdex host's look
// (`useHostLookStore`, through `lib/host-look.ts` — the tab badge shows the same one); a host without one, and every indicator, in `usePoolHostsStore`.
import { useCallback, useMemo } from 'react'
import { lookKeyOf, useHostStore } from '../../stores/useHostStore'
import { useHostLookStore, type HostLookEntry } from '../../stores/useHostLookStore'
import { usePoolHostsStore, type PoolHostIndicator, type PoolHostLook } from '../../stores/usePoolHostsStore'
import { useUISettingsStore, type FleetCellIndicator } from '../../stores/useUISettingsStore'
import { useHostLookResolver, hostLookOf, type HostLook } from '../host-look'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { poolHostOwners, readPoolHostOwners, usePoolHostOwners } from './daemons'

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
  indicator?: PoolHostIndicator | null
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

/** A look with none of its colour or icon fields: what `resolve` gives a daemon host nobody has styled yet. */
const hasFace = (l: Pick<HostLook, 'colors' | 'color' | 'icon' | 'iconWeight'> | undefined): boolean =>
  l !== undefined && (l.colors !== undefined || l.color !== undefined || l.icon !== undefined || l.iconWeight !== undefined)

/**
 * For lists (a hook cannot run in a loop): a resolver over the owners, the host looks, the pool store's looks and the global indicator default, stable while they are.
 * The owner is the host that has been seen to front this wwd, connected or not: where the look lives must not flip with a reconnect.
 */
export function usePoolHostLookResolver(): (hostId: string) => PoolHostResolvedLook {
  const owners = usePoolHostOwners()
  const resolve = useHostLookResolver()
  const looks = usePoolHostsStore((s) => s.looks)
  const global = useUISettingsStore((s) => s.fleetCellIndicator)
  return useCallback((hostId: string) => {
    const stored = Object.hasOwn(looks, hostId) ? looks[hostId] : undefined
    const owner = owners[hostId]
    const fromHost = owner !== undefined ? resolve(owner) : undefined
    // A face written to the pool store before the owner was known (first boot) is shown until the host's own look has one.
    return poolLookOf(stored, fromHost, global)
  }, [owners, resolve, looks, global])
}

/** A face written to the pool store before the owner was known (first boot) is shown until the host's own look has one. */
function poolLookOf(stored: PoolHostLook | undefined, fromHost: HostLook | undefined, global: FleetCellIndicator): PoolHostResolvedLook {
  const base = fromHost !== undefined && hasFace(fromHost) ? fromHost : stored
  return compose(base, indicatorOf(stored, global))
}

const NO_ORDER: string[] = []
const NO_ROWS: Record<string, never> = {}

/**
 * One pool host's look for a surface that is drawn once per tab (a tab row): the same answer as `usePoolHostLook`, but subscribed to the stores only while `hostId` is not empty.
 * With `''` (the tab is not a pool host's) every selector gives a constant, so an unrelated write to a host store renders nothing.
 */
export function useTabPoolHostLook(hostId: string): PoolHostResolvedLook {
  const on = hostId !== ''
  const hostOrder = useHostStore((s) => (on ? s.hostOrder : NO_ORDER))
  const hosts = useHostStore((s) => (on ? s.hosts : NO_ROWS))
  const byHost = useNexHostStore((s) => (on ? s.byHost : NO_ROWS))
  const hostLooks = useHostLookStore((s) => (on ? s.looks : NO_ROWS))
  const stored = usePoolHostsStore((s) => (on && Object.hasOwn(s.looks, hostId) ? s.looks[hostId] : undefined))
  const global = useUISettingsStore((s) => s.fleetCellIndicator)
  return useMemo(() => {
    const owner = on ? poolHostOwners({ hostOrder, byHost })[hostId] : undefined
    const fromHost = owner !== undefined ? hostLookOf(owner, hosts, hostLooks) : undefined
    return poolLookOf(stored, fromHost, global)
  }, [on, hostId, hostOrder, byHost, hosts, hostLooks, stored, global])
}

export function usePoolHostLook(hostId: string): PoolHostResolvedLook {
  const resolve = usePoolHostLookResolver()
  return useMemo(() => resolve(hostId), [resolve, hostId])
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
  const daemon = readPoolHostOwners()[hostId]
  const { indicator, ...face } = patch
  if (indicator !== undefined) {
    usePoolHostsStore.getState().patchLook(hostId, (cur) => {
      if (indicator !== null) return { ...cur, indicator }
      if (cur?.indicator === undefined) return cur
      const { indicator: _gone, ...rest } = cur
      void _gone
      return rest
    })
  }
  if (Object.keys(face).length === 0) return
  if (daemon !== undefined) {
    const host = useHostStore.getState().hosts[daemon]
    if (!host) return
    const key = lookKeyOf(host, useHostLookStore.getState().looks)
    useHostLookStore.getState().patchLook(key, (cur) => editFace(cur, face))
    // The host's own look is the home now: a face written to the pool store before its owner was known goes (the indicator stays), so a later reset to default cannot bring it back.
    usePoolHostsStore.getState().patchLook(hostId, (cur) => {
      if (cur === undefined || !hasFace(cur)) return cur
      const { colors: _c, color: _co, icon: _i, iconWeight: _w, ...rest } = cur
      void [_c, _co, _i, _w]
      return Object.keys(rest).length > 0 ? rest : undefined
    })
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
