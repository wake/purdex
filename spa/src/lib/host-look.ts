// spa/src/lib/host-look.ts — THE read path of a host's look (name, colours,
// icon) for every screen (host-ownership spec §4.2, H2 plan H2a / H2c-2).
//
// A screen never reads `HostConfig.name / colors / color / icon / iconWeight`
// directly; it asks this selector. From H2c-2 the per-workbench look store
// (`useHostLookStore`, keyed by wire id) is the SOT: a local host reads the
// entry under `wireIdOfHost(host)` (its `d1_…` when it has a valid daemonId,
// else its local id — plan §0.4); a ref that is no local host reads the entry
// under the ref itself. `host-look.guard.test.ts` holds the line.
//
// Fallback (plan §0.5, option A — a deviation from spec §4.2's "field by
// field"): once an entry exists, the colour group (`colors` + legacy `color`)
// and the icon group (`icon` + `iconWeight`) come from the entry only — absent
// = no colour / default icon; `name` alone still falls back to
// `HostConfig.name`. No entry → every field from `HostConfig`.
//
// Placement: this module imports `useHostStore` and `useHostLookStore`;
// nothing either of them imports may import this module.
import { useCallback } from 'react'
import { useHostStore, type HostConfig } from '../stores/useHostStore'
import { useHostLookStore, type HostLookEntry } from '../stores/useHostLookStore'
import { wireIdOfHost } from './profile/host-identity'
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

function entryOf(looks: Record<string, HostLookEntry>, key: string | null): HostLookEntry | undefined {
  if (key === null || !Object.hasOwn(looks, key)) return undefined
  const entry = looks[key]
  return entry !== null && typeof entry === 'object' ? entry : undefined
}

/** Per entry, per fallback host (or none): the composed look, stable while both objects are. */
const entryMemo = new WeakMap<HostLookEntry, { bare?: HostLook; byHost: WeakMap<HostConfig, HostLook> }>()

/** The look of an entry (option A): its own fields; only `name` falls back to `fallbackName`. */
function composeEntry(entry: HostLookEntry, fallbackName: string | undefined): HostLook {
  const look: HostLook = {}
  const name = entry.name ?? fallbackName
  if (name !== undefined) look.name = name
  if (entry.colors !== undefined) look.colors = entry.colors
  if (entry.color !== undefined) look.color = entry.color
  if (entry.icon !== undefined) look.icon = entry.icon
  if (entry.iconWeight !== undefined) look.iconWeight = entry.iconWeight
  return look
}

function lookOfEntry(entry: HostLookEntry, host: HostConfig | undefined): HostLook {
  let slot = entryMemo.get(entry)
  if (slot === undefined) {
    slot = { byHost: new WeakMap() }
    entryMemo.set(entry, slot)
  }
  if (host === undefined) return (slot.bare ??= composeEntry(entry, undefined))
  let look = slot.byHost.get(host)
  if (look === undefined) {
    // Only the name is read off the host: the colour / icon groups are the entry's (option A).
    look = composeEntry(entry, lookOfHost(host).name)
    slot.byHost.set(host, look)
  }
  return look
}

/** The key a ref's look lives under: a local host's wire id, else the ref itself. */
function keyOf(host: HostConfig | undefined, ref: string | null): string | null {
  return host === undefined ? ref : wireIdOfHost(host)
}

function resolveLook(host: HostConfig | undefined, entry: HostLookEntry | undefined): HostLook {
  if (entry !== undefined) return lookOfEntry(entry, host)
  return host === undefined ? NO_LOOK : lookOfHost(host)
}

/**
 * The look of `ref` — a local id, or a wire id no local host claims (its entry
 * only). Unknown and no entry → `{}`. `hosts` / `looks` default to the current
 * store snapshots.
 */
export function hostLookOf(
  ref: string,
  hosts: Record<string, HostConfig> = useHostStore.getState().hosts,
  looks: Record<string, HostLookEntry> = useHostLookStore.getState().looks,
): HostLook {
  const host = hostOf(hosts, ref)
  return resolveLook(host, entryOf(looks, keyOf(host, ref)))
}

/**
 * `hostLookOf` as a hook. Subscribes to the ONE host object and the ONE entry
 * under its key (recomputed from the host), so it re-renders on those writes
 * only — not on another host's or entry's, nor on `runtime`.
 */
export function useHostLook(ref: string | null): HostLook {
  const host = useHostStore((s) => hostOf(s.hosts, ref))
  const key = keyOf(host, ref)
  const entry = useHostLookStore((s) => entryOf(s.looks, key))
  return resolveLook(host, entry)
}

/**
 * For lists (a hook cannot run in a loop): a resolver over the current hosts
 * and looks, stable while both are (a `runtime` write does not change it).
 */
export function useHostLookResolver(): (ref: string) => HostLook {
  const hosts = useHostStore((s) => s.hosts)
  const looks = useHostLookStore((s) => s.looks)
  return useCallback((ref: string) => hostLookOf(ref, hosts, looks), [hosts, looks])
}

/** What to call a host: its look name, else the ref itself. */
export function hostLabel(ref: string, look: HostLook): string {
  return look.name ?? ref
}
