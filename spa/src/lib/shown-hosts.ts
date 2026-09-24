// spa/src/lib/shown-hosts.ts — which hosts the workbench enables: the host selector AND the pane matcher (host
// ownership spec §4.5; plan H2d-1, §0.21 / §0.23).
//
// The store (`useShownHostsStore`) holds WIRE ids. Two rules read it, on purpose different:
// - `isHostShown(hostId, …)` — for the "open a tab" surfaces (New Tab, the Hosts page, the landings; H2d-4 / H2d-5),
//   which only ever name LOCAL hosts: a local host is shown when `all`, or its `wireIdOfHost` is listed. An id that is
//   not a local host → `true` (navigation to it is not this module's business).
// - the pane matcher — `hostRefOf` / `wireOfRef` / `isRefEnabled` / `isPaneHostEnabled` (+ the live hook and the
//   non-hook read) — for the disable planner (H2d-2) and the pane gate / per-pane sweeps (H2d-4b). A pane's host ref is
//   a local id, or a wire id (`d1_…`) this device has not resolved (H1a keeps those); both are mapped into WIRE space
//   and a ref is enabled when `all`, or its wire id is listed — so a pane on an unresolved `d1_X` is disabled when
//   `d1_X` is not listed, where `isHostShown('d1_X')` says `true`.
//
// Placement: this module imports `useHostStore`, `useShownHostsStore` and `host-look` (`wireKeyMovesOf`); none of them
// may import this module.
import { useCallback } from 'react'
import { useHostStore, type HostConfig } from '../stores/useHostStore'
import { useShownHostsStore, type ShownHosts } from '../stores/useShownHostsStore'
import { wireIdOfHost } from './profile/host-identity'
import { wireKeyMovesOf } from './host-look'
import type { PaneContent } from '../types/tab'

/** What the rules read of the store: `all` and the listed wire ids. */
export type ShownHostsView = Pick<ShownHosts, 'all'> & { readonly ids: readonly string[] }

function localHostOf(hosts: Record<string, HostConfig>, id: string): HostConfig | undefined {
  if (!Object.hasOwn(hosts, id)) return undefined
  const host = hosts[id]
  return host !== null && typeof host === 'object' ? host : undefined
}

// === The host selector ("open a tab" surfaces) ===

/** A LOCAL host is shown when `all`, or its wire id is listed; an id that is not a local host → `true`. Pure. */
export function isHostShown(hostId: string, hosts: Record<string, HostConfig>, shown: ShownHostsView): boolean {
  if (shown.all) return true
  const host = localHostOf(hosts, hostId)
  return host === undefined || shown.ids.includes(wireIdOfHost(host))
}

/** `isHostShown` for one host, live: re-renders on a shown-hosts write or a change of that host (daemonId learned). */
export function useIsHostShown(hostId: string | null): boolean {
  const host = useHostStore((s) => (hostId === null ? undefined : localHostOf(s.hosts, hostId)))
  const wire = host === undefined ? null : wireIdOfHost(host)
  return useShownHostsStore((s) => wire === null || s.all || s.ids.includes(wire))
}

/** For lists: `(hostId) => isHostShown(…)` over the current stores, stable while they are. */
export function useShownHostFilter(): (hostId: string) => boolean {
  const hosts = useHostStore((s) => s.hosts)
  const all = useShownHostsStore((s) => s.all)
  const ids = useShownHostsStore((s) => s.ids)
  return useCallback((hostId: string) => isHostShown(hostId, hosts, { all, ids }), [hosts, all, ids])
}

// === The pane matcher (wire space; §0.21 "What is a tab of X", §0.23) ===

/**
 * The host a pane stands on, or `null` when it is not host-bearing: tmux → `hostId` (terminated included); execution
 * → `host`, else `hostOrder[0]` (the `resolveExecutionHostId` rule — an empty hint is no hint; `null` when there is no
 * host at all); every other kind — a daemon-source editor / preview included (§0.22 (a)) — → `null`.
 */
export function hostRefOf(content: PaneContent, hostOrder: readonly string[]): string | null {
  switch (content.kind) {
    case 'tmux-session':
      return content.hostId
    case 'execution':
      return content.host || (hostOrder[0] ?? null)
    default:
      return null
  }
}

/** A ref in WIRE space: a local host → its `wireIdOfHost`; any other ref (an unresolved `d1_…`) → itself. */
export function wireOfRef(ref: string, hosts: Record<string, HostConfig>): string {
  const host = localHostOf(hosts, ref)
  return host === undefined ? ref : wireIdOfHost(host)
}

/** A host ref is enabled when `all`, or its wire id is listed. Pure. */
export function isRefEnabled(ref: string, hosts: Record<string, HostConfig>, shown: ShownHostsView): boolean {
  return shown.all || shown.ids.includes(wireOfRef(ref, hosts))
}

/** A pane is enabled when it is not host-bearing, or its host ref is enabled. Pure. */
export function isPaneHostEnabled(
  content: PaneContent,
  hosts: Record<string, HostConfig>,
  hostOrder: readonly string[],
  shown: ShownHostsView,
): boolean {
  const ref = hostRefOf(content, hostOrder)
  return ref === null || isRefEnabled(ref, hosts, shown)
}

/**
 * `isPaneHostEnabled` as a hook, subscribed LIVE to both stores (hosts + `hostOrder`, and the shown hosts): it
 * re-renders when the pane's wire id moves (a daemonId learned, `hostOrder[0]` changed) or the list changes. Each
 * selector returns a primitive — no new object per render.
 */
export function usePaneHostEnabled(content: PaneContent): boolean {
  const wire = useHostStore((s) => {
    const ref = hostRefOf(content, s.hostOrder)
    return ref === null ? null : wireOfRef(ref, s.hosts)
  })
  return useShownHostsStore((s) => wire === null || s.all || s.ids.includes(wire))
}

/** The non-hook read for the per-pane sweeps: `isRefEnabled` over both stores' current state. */
export function isHostRefEnabledNow(ref: string): boolean {
  return isRefEnabled(ref, useHostStore.getState().hosts, useShownHostsStore.getState())
}

// === Re-key (spec §4.3, plan §0.12; run by the re-resolve pass) ===

/**
 * The pass's shown-hosts step: a listed local id of a host whose daemonId is known becomes its `d1_…` id, in place;
 * when that `d1_…` is already listed the local id is dropped (`useShownHostsStore.rekey`). Same moves as the look
 * re-key (`wireKeyMovesOf` — a daemon two rows claim moves nothing). `null` when nothing would move; else the write
 * and its way back, for the pass to commit under its lock. Never part of the explicit-map rewrite a deletion reuses.
 */
export function rekeyShownHosts(hosts: Record<string, HostConfig>): { commit: () => void; undo: () => void } | null {
  const { all, ids } = useShownHostsStore.getState()
  const moves = wireKeyMovesOf(hosts).filter(([from]) => ids.includes(from))
  if (moves.length === 0) return null
  return {
    commit: () => useShownHostsStore.getState().rekey(moves),
    undo: () => useShownHostsStore.setState({ all, ids }),
  }
}
