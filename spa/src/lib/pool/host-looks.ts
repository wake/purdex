// spa/src/lib/pool/host-looks.ts — a pool host's look and order for the monitoring screens (monitoring tabs spec A3, M9, M13, W7).
// A pool host is keyed by the wwd `host_id`. Its colour and icon live where the rest of the App already reads them: a host with a daemon in the Purdex host's look
// (`useHostLookStore`, through `lib/host-look.ts` — the tab badge shows the same one); a host without one, and every indicator, in `usePoolHostsStore`.
import { useCallback, useMemo, useSyncExternalStore } from 'react'
import { lookKeyOf, useHostStore } from '../../stores/useHostStore'
import { useHostLookStore, type HostLookEntry } from '../../stores/useHostLookStore'
import { usePoolHostsStore, type PoolHostIndicator, type PoolHostLook } from '../../stores/usePoolHostsStore'
import { useHostLook, useHostLookResolver, type HostLook } from '../host-look'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { readPoolHostOwners, usePoolHostOwners } from './daemons'

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
  /** The legacy single colour: only ever cleared (`null`), by an edit that replaces it with `colors`. */
  color?: string | null
  icon?: string | null
  iconWeight?: HostLook['iconWeight'] | null
  indicator?: PoolHostIndicator | null
}

/** M51: a host that has not set an indicator shows the ring on the overall load. */
const DEFAULT_INDICATOR: PoolHostIndicator = { shape: 'ring', data: ['load'] }

export function indicatorOf(look: Pick<PoolHostLook, 'indicator'> | undefined): PoolHostIndicator {
  return look?.indicator ?? DEFAULT_INDICATOR
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
 * For lists (a hook cannot run in a loop): a resolver over the owners, the host looks and the pool store's looks, stable while they are.
 * The owner is the host that has been seen to front this wwd, connected or not: where the look lives must not flip with a reconnect.
 */
export function usePoolHostLookResolver(): (hostId: string) => PoolHostResolvedLook {
  const owners = usePoolHostOwners()
  const resolve = useHostLookResolver()
  const looks = usePoolHostsStore((s) => s.looks)
  return useCallback((hostId: string) => {
    const stored = Object.hasOwn(looks, hostId) ? looks[hostId] : undefined
    const owner = owners[hostId]
    const fromHost = owner !== undefined ? resolve(owner) : undefined
    // A face written to the pool store before the owner was known (first boot) is shown until the host's own look has one.
    return poolLookOf(stored, fromHost)
  }, [owners, resolve, looks])
}

/** A face written to the pool store before the owner was known (first boot) is shown until the host's own look has one. */
function poolLookOf(stored: PoolHostLook | undefined, fromHost: HostLook | undefined): PoolHostResolvedLook {
  const base = fromHost !== undefined && hasFace(fromHost) ? fromHost : stored
  return compose(base, indicatorOf(stored))
}

/** The App host that fronts a pool host, as a primitive: a tab row re-renders only when this changes, not on every write to the host stores it is computed from. */
function useOwnerOf(hostId: string): string | undefined {
  return useSyncExternalStore(
    (notify) => {
      const offHosts = useHostStore.subscribe(notify)
      const offNex = useNexHostStore.subscribe(notify)
      return () => { offHosts(); offNex() }
    },
    () => (hostId === '' ? undefined : readPoolHostOwners()[hostId]),
  )
}

/**
 * One pool host's look for a surface that is drawn once per tab (a tab row): the same answer as `usePoolHostLook`, but it subscribes to the one owner, the one host and the one look entry,
 * so a write about another host renders nothing. With `''` (the tab is not a pool host's) it subscribes to nothing that changes.
 */
export function useTabPoolHostLook(hostId: string): PoolHostResolvedLook {
  const owner = useOwnerOf(hostId)
  const fromOwner = useHostLook(owner ?? null)
  const stored = usePoolHostsStore((s) => (hostId !== '' && Object.hasOwn(s.looks, hostId) ? s.looks[hostId] : undefined))
  return useMemo(() => poolLookOf(stored, owner !== undefined ? fromOwner : undefined), [stored, owner, fromOwner])
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
  applyField(next, 'color', face.color)
  applyField(next, 'icon', face.icon)
  applyField(next, 'iconWeight', face.iconWeight)
  return next as T
}
