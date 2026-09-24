// spa/src/lib/shown-hosts.ts — which hosts the workbench shows: the ONE shown predicate, the pane matcher, the writer
// and the re-key (host ownership spec §1.2 / §4.5; plan H2d-1, §0.21 "What is a pane on X" / "One rule for every opener
// too").
//
// The store (`useShownHostsStore`) holds WIRE ids — `d1_…`, or a host's local id until its daemonId is known and the
// re-resolve pass re-keys it. A ref (a pane's host, an opener's host) is a LOCAL id, or a wire id this device has not
// resolved (H1a keeps those). ONE rule reads it, for the pane gate, the sweeps AND every opener:
//   `isRefShown(ref)` — a local host → one of its forms (`shownFormsOf`: its wire id, its local id) is listed;
//                       any other ref → the ref itself is listed.
// There is deliberately no "not a local host → shown" helper: with one, `/execution/d1_X/<id>` opened a tab that the
// gate then hid (codex plan review task-mufjfxo4-h4e2rf item 1).
//
// WHICH LIST (per-workbench shown hosts, 2026-09-25 plan A2): every workbench has its own. The one that applies is the
// list of the world the live tab stores hold — `useTabStore.worldId`, the tag every switch and promote stamps: the master
// → `useShownHostsStore.ids`; a local workbench → its `LocalProfile.shownHostIds`. FAIL CLOSED: `[]` (every host hidden:
// gated, never connected) unless, on EVERY read, the world is settled (`readMasterWorld` — the same check the collector
// uses: tags, epochs, the fence), and on the master the shown store's `relabelStamp` is the local profiles'
// `relabelCount` (a promote in another window that has reached one of the two stores but not the other). A hidden pane
// opens no connection, so a transient `[]` can only delay one, never open a wrong one.
//
// Placement: this module imports `useHostStore`, `useShownHostsStore`, `useLocalProfilesStore`, `useTabStore` (the world
// tag; the landing, H2d-3), `useWorkspaceStore` (its tag, for the hooks' subscription), `profile/master-world` (the
// settled check) and `host-look` (`wireKeyMovesOf`); none of them may import this module.
import { useCallback, useSyncExternalStore } from 'react'
import { useWorkspaceStore } from '../features/workspace/store'
import { useHostStore, type HostConfig } from '../stores/useHostStore'
import { MASTER_PROFILE_ID, useLocalProfilesStore, type LocalProfile } from '../stores/useLocalProfilesStore'
import { useShownHostsStore, type ShownHosts } from '../stores/useShownHostsStore'
import { useTabStore } from '../stores/useTabStore'
import { readMasterWorld } from './profile/master-world'
import { wireIdOfHost } from './profile/host-identity'
import { wireKeyMovesOf } from './host-look'
import type { PaneContent } from '../types/tab'

type Hosts = Record<string, HostConfig>
type Ids = readonly string[]

function localHostOf(hosts: Hosts, id: string): HostConfig | undefined {
  if (!Object.hasOwn(hosts, id)) return undefined
  const host = hosts[id]
  return host !== null && typeof host === 'object' ? host : undefined
}

/** The ids under which a host can be listed: its wire id and — until the re-key — its local id; deduped. */
export function shownFormsOf(host: HostConfig): string[] {
  const wire = wireIdOfHost(host)
  return wire === host.id ? [wire] : [wire, host.id]
}

/** A ref in WIRE space: a local host → its `wireIdOfHost`; any other ref (an unresolved `d1_…`) → itself. */
export function wireOfRef(ref: string, hosts: Hosts): string {
  const host = localHostOf(hosts, ref)
  return host === undefined ? ref : wireIdOfHost(host)
}

/** The primitive pair a live selector compares: the wire form, and the local id when the ref is a local host. */
function formsOfRef(ref: string, hosts: Hosts): { wire: string; local: string | null } {
  const host = localHostOf(hosts, ref)
  return host === undefined ? { wire: ref, local: null } : { wire: wireIdOfHost(host), local: host.id }
}

function listed(ids: Ids, wire: string, local: string | null): boolean {
  if (wire === '') return false // no host at all is never shown
  return ids.includes(wire) || (local !== null && ids.includes(local))
}

/** THE predicate. A local host → its wire id or its local id is listed; any other ref → it is listed. Pure. */
export function isRefShown(ref: string, hosts: Hosts, ids: Ids): boolean {
  const { wire, local } = formsOfRef(ref, hosts)
  return listed(ids, wire, local)
}

// === Which list (the current workbench's) ===

/** The fail-closed answer: one frozen reference, so a selector that returns it is stable. */
const NONE: Ids = Object.freeze([])

/**
 * The shown list of the workbench on screen. Pure. `settled` — `readMasterWorld().settled`; `tabWorldId` — the live tab
 * store's world tag. The master → `shown.ids` while `shown.relabelStamp === local.relabelCount`; a slave → its record's
 * list; anything else → `[]`. Always a reference one of the stores holds, or `NONE`: never a fresh array.
 */
export function currentShownIds(
  settled: boolean,
  tabWorldId: unknown,
  local: { slaves: Record<string, LocalProfile>; relabelCount: number },
  shown: ShownHosts,
): Ids {
  if (!settled || typeof tabWorldId !== 'string') return NONE
  if (tabWorldId === MASTER_PROFILE_ID) return shown.relabelStamp === local.relabelCount ? shown.ids : NONE
  return Object.hasOwn(local.slaves, tabWorldId) ? local.slaves[tabWorldId].shownHostIds : NONE
}

/** `currentShownIds` over the stores as they are now — the settled check made again on every call. */
export function currentShownIdsNow(): Ids {
  return currentShownIds(readMasterWorld().settled, useTabStore.getState().worldId, useLocalProfilesStore.getState(), useShownHostsStore.getState())
}

/** Every store the current list depends on: the tab and workspace tags, the local profiles, the master's list. */
function subscribeCurrentShown(fn: () => void): () => void {
  const unsubs = [useTabStore.subscribe(fn), useWorkspaceStore.subscribe(fn), useLocalProfilesStore.subscribe(fn), useShownHostsStore.subscribe(fn)]
  return () => {
    for (const unsub of unsubs) unsub()
  }
}

/** The current list, live. NOTE: a window that falls behind the epoch fence (another window's switch, before any of
 *  its stores rehydrates here) is not re-read until one of the four stores changes — accepted (plan A2, like the
 *  collector); `isRefShownNow` re-reads every time. */
function useCurrentShownIds(): Ids {
  return useSyncExternalStore(subscribeCurrentShown, currentShownIdsNow, currentShownIdsNow)
}

/** `isRefShown` over the current stores — for openers, sweeps and in-flight re-checks. */
export function isRefShownNow(ref: string): boolean {
  return isRefShown(ref, useHostStore.getState().hosts, currentShownIdsNow())
}

/** `isRefShown` for one ref, live: re-renders when its ANSWER changes — a write to the current list, a world switch,
 *  a world that stops (or starts) being settled, a daemonId learned. */
export function useIsRefShown(ref: string | null): boolean {
  const wire = useHostStore((s) => (ref === null ? null : formsOfRef(ref, s.hosts).wire))
  const local = useHostStore((s) => (ref === null ? null : formsOfRef(ref, s.hosts).local))
  const answer = (): boolean => wire !== null && listed(currentShownIdsNow(), wire, local)
  return useSyncExternalStore(subscribeCurrentShown, answer, answer)
}

/** For lists: `(ref) => isRefShown(…)` over the current stores, stable while the hosts and the current list are. */
export function useShownRefFilter(): (ref: string) => boolean {
  const hosts = useHostStore((s) => s.hosts)
  const ids = useCurrentShownIds()
  return useCallback((ref: string) => isRefShown(ref, hosts, ids), [hosts, ids])
}

// === The landing (H2d-3: notification, deep link, route, the Handoff toast) ===

/**
 * The landing of an opener that would create or focus a tab on `ref`. Shown (`isRefShownNow`) → `false`, nothing done
 * — the caller opens its tab. Otherwise → the Hosts page and `true`, never a tab: a hidden LOCAL host → the Hosts page
 * on that host (the `open-host` notification action's body); any other ref — an unlisted `d1_X`, a deleted host's id,
 * `''` from a hostless link with no host — is not openable → the Hosts page, `activeHostId` unchanged.
 */
export function landOnHostsPageIfHidden(ref: string): boolean {
  if (isRefShownNow(ref)) return false
  useTabStore.getState().openSingletonTab({ kind: 'hosts' })
  if (localHostOf(useHostStore.getState().hosts, ref) !== undefined) useHostStore.getState().setActiveHost(ref)
  return true
}

// === The pane matcher ===

/**
 * The host a pane stands on, or `null` when it is not host-bearing: tmux → `hostId` (terminated included); execution
 * → `host`, else `hostOrder[0]` (the `resolveExecutionHostId` rule — an empty hint is no hint; `null` when there is no
 * host at all); every other kind — a daemon-source editor / preview included (§0.22, DECIDED) — → `null`.
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

/** A pane is shown when it is not host-bearing, or `isRefShown` of its host ref. Pure. */
export function isPaneHostShown(content: PaneContent, hosts: Hosts, hostOrder: readonly string[], ids: Ids): boolean {
  const ref = hostRefOf(content, hostOrder)
  return ref === null || isRefShown(ref, hosts, ids)
}

/**
 * `isPaneHostShown` as a hook, subscribed LIVE to the host store (hosts + `hostOrder`) and to everything the current
 * list depends on (`useCurrentShownIds`'s stores): it re-renders when the pane's forms move (a daemonId learned,
 * `hostOrder[0]` changed) or its answer changes. Each selector returns a primitive — no new object per render.
 */
export function usePaneHostShown(content: PaneContent): boolean {
  const ref = useHostStore((s) => hostRefOf(content, s.hostOrder))
  const wire = useHostStore((s) => (ref === null ? null : formsOfRef(ref, s.hosts).wire))
  const local = useHostStore((s) => (ref === null ? null : formsOfRef(ref, s.hosts).local))
  const answer = (): boolean => wire === null || listed(currentShownIdsNow(), wire, local)
  return useSyncExternalStore(subscribeCurrentShown, answer, answer)
}

// === The writer (the Hosts page switch, H2d-2) ===

/**
 * Show / hide ONE local host in the workbench: shown → its wire id appended; hidden → every form of it removed
 * (`shownFormsOf`). Never touches another id; an unknown host is a no-op.
 */
export function setHostShown(hostId: string, shown: boolean): void {
  const host = localHostOf(useHostStore.getState().hosts, hostId)
  if (host === undefined) return
  const store = useShownHostsStore.getState()
  if (shown) {
    store.show(wireIdOfHost(host))
    return
  }
  for (const form of shownFormsOf(host)) useShownHostsStore.getState().hide(form)
}

// === Re-key (spec §4.3, plan §0.12; run by the re-resolve pass) ===

/**
 * The pass's shown-hosts step: a listed local id of a host whose daemonId is known becomes its `d1_…` id, in place;
 * when that `d1_…` is already listed the local id is dropped (`useShownHostsStore.rekey`). Same moves as the look
 * re-key (`wireKeyMovesOf` — a daemon two rows claim moves nothing). `null` when nothing would move; else the write
 * and its way back, for the pass to commit under its lock. Never part of the explicit-map rewrite a deletion reuses.
 */
export function rekeyShownHosts(hosts: Hosts): { commit: () => void; undo: () => void } | null {
  const { ids } = useShownHostsStore.getState()
  const moves = wireKeyMovesOf(hosts).filter(([from]) => ids.includes(from))
  if (moves.length === 0) return null
  return {
    commit: () => useShownHostsStore.getState().rekey(moves),
    undo: () => useShownHostsStore.setState({ ids }),
  }
}
