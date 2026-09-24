// spa/src/lib/host-look.ts — THE read path of a host's look (name, colours,
// icon) for every screen (host-ownership spec §4.2, H2 plan H2a).
//
// A screen never reads `HostConfig.name / colors / color / icon / iconWeight`
// directly; it asks this selector. Today (H2a/H2b) the look comes from
// `HostConfig` only; H2c puts the per-workbench look store in front of it
// without touching any screen. `host-look.guard.test.ts` holds the line.
//
// Placement: this module imports `useHostStore`; nothing `useHostStore` (or,
// in H2c, the look store) imports may import this module.
import { useCallback } from 'react'
import { useHostStore, type HostConfig } from '../stores/useHostStore'
import type { IconWeight } from '../types/tab'

/** A host's look: only the fields that are present. */
export interface HostLook {
  name?: string
  colors?: HostConfig['colors']
  /** @deprecated legacy single colour; read-only (host-color-modes D10). */
  color?: string
  icon?: string
  iconWeight?: IconWeight
}

/** The look of a ref nobody knows. Frozen: shared by every caller. */
const NO_LOOK: HostLook = Object.freeze({})

/** Per host object: equal input → the same look object (so `useMemo` deps and `===` hold). */
const lookMemo = new WeakMap<HostConfig, HostLook>()

function lookOfHost(host: HostConfig): HostLook {
  let look = lookMemo.get(host)
  if (look === undefined) {
    const { name, colors, color, icon, iconWeight } = host
    const next: HostLook = {}
    if (name !== undefined) next.name = name
    if (colors !== undefined) next.colors = colors
    if (color !== undefined) next.color = color
    if (icon !== undefined) next.icon = icon
    if (iconWeight !== undefined) next.iconWeight = iconWeight
    look = next
    lookMemo.set(host, look)
  }
  return look
}

function hostOf(hosts: Record<string, HostConfig>, ref: string | null): HostConfig | undefined {
  if (ref === null || !Object.hasOwn(hosts, ref)) return undefined
  const host = hosts[ref]
  return host !== null && typeof host === 'object' ? host : undefined
}

/**
 * The look of `ref` (a local id; from H2c also a wire id). Unknown → `{}`.
 * `hosts` defaults to the current store snapshot.
 */
export function hostLookOf(ref: string, hosts: Record<string, HostConfig> = useHostStore.getState().hosts): HostLook {
  const host = hostOf(hosts, ref)
  return host === undefined ? NO_LOOK : lookOfHost(host)
}

/**
 * `hostLookOf` as a hook. Subscribes to the ONE host object, so it re-renders
 * on that host's writes only — not on another host's, nor on `runtime`.
 */
export function useHostLook(ref: string | null): HostLook {
  const host = useHostStore((s) => hostOf(s.hosts, ref))
  return host === undefined ? NO_LOOK : lookOfHost(host)
}

/**
 * For lists (a hook cannot run in a loop): a resolver over the current hosts,
 * stable while `hosts` is (a `runtime` write does not change it).
 */
export function useHostLookResolver(): (ref: string) => HostLook {
  const hosts = useHostStore((s) => s.hosts)
  return useCallback((ref: string) => hostLookOf(ref, hosts), [hosts])
}

/** What to call a host: its look name, else the ref itself. */
export function hostLabel(ref: string, look: HostLook): string {
  return look.name ?? ref
}
