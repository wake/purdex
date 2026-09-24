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
import { rekeyShownIds, useShownHostsStore, type ShownHosts } from '../stores/useShownHostsStore'
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

type LocalView = { slaves: Record<string, LocalProfile>; relabelCount: number }

/** Where a workbench's list lives: the master's in `useShownHostsStore`, a local workbench's on its record. */
export type ShownOwner = { kind: 'master' } | { kind: 'slave'; id: string }

/**
 * THE ONE ANSWER to "whose list is this, and can it be read or written right now" (pure) — the reader, the writer
 * (`setHostShown`) and the copies (switch-active.ts) all ask it, so none of them can be laxer than another. `worldId` —
 * the world asked about: the live tab tag for the workbench on screen, `'master'` for the master's own list.
 *   the master → `master` only while `shown.relabelStamp === local.relabelCount` (a promote in another window that
 *                has reached one of the two stores but not the other: neither list is the master's for sure);
 *   a slave    → that slave, while its record exists;
 *   else — not settled (`readMasterWorld`), a tag that is no string, an unknown id → `null`: nobody can say.
 */
export function resolveShownOwner(settled: boolean, worldId: unknown, local: LocalView, shown: ShownHosts): ShownOwner | null {
  if (!settled || typeof worldId !== 'string') return null
  if (worldId === MASTER_PROFILE_ID) return shown.relabelStamp === local.relabelCount ? { kind: 'master' } : null
  return Object.hasOwn(local.slaves, worldId) ? { kind: 'slave', id: worldId } : null
}

/** `resolveShownOwner` over the stores as they are now: for `worldId`, or — omitted — the workbench on screen. */
export function resolveShownOwnerNow(worldId: unknown = useTabStore.getState().worldId): ShownOwner | null {
  return resolveShownOwner(readMasterWorld().settled, worldId, useLocalProfilesStore.getState(), useShownHostsStore.getState())
}

function listOf(owner: ShownOwner | null, local: LocalView, shown: ShownHosts): Ids {
  if (owner === null) return NONE
  return owner.kind === 'master' ? shown.ids : local.slaves[owner.id].shownHostIds
}

/**
 * The shown list of the workbench on screen. Pure. `settled` — `readMasterWorld().settled`; `tabWorldId` — the live tab
 * store's world tag. The list of `resolveShownOwner`'s answer, or `[]` when it is `null`. Always a reference one of the
 * stores holds, or `NONE`: never a fresh array.
 */
export function currentShownIds(settled: boolean, tabWorldId: unknown, local: LocalView, shown: ShownHosts): Ids {
  return listOf(resolveShownOwner(settled, tabWorldId, local, shown), local, shown)
}

/** `currentShownIds` over the stores as they are now — the settled check made again on every call. */
export function currentShownIdsNow(): Ids {
  return currentShownIds(readMasterWorld().settled, useTabStore.getState().worldId, useLocalProfilesStore.getState(), useShownHostsStore.getState())
}

/** THE MASTER'S list, wherever the master is — `null` when nobody can say (`resolveShownOwner` for `'master'`). For a
 *  copy of the master: the list of another label must never be copied as its own. */
export function masterShownIdsNow(): Ids | null {
  const local = useLocalProfilesStore.getState()
  const shown = useShownHostsStore.getState()
  const owner = resolveShownOwner(readMasterWorld().settled, MASTER_PROFILE_ID, local, shown)
  return owner === null ? null : listOf(owner, local, shown)
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
 * Show / hide ONE local host in the workbench ON SCREEN: shown → its wire id appended; hidden → every form of it
 * removed (`shownFormsOf`). Never touches another id. Which list: exactly the one the reader reads
 * (`resolveShownOwnerNow`): the master → `useShownHostsStore`; a local workbench → its record (`setSlaveShownHosts`),
 * never the master's store (which syncs). `false` = nothing written: an unknown host, or no owner — a world that is not
 * settled, a promote half-arrived from another window, a slave record that is gone (the reader shows `[]` then, and a
 * write could land in the wrong workbench's list).
 */
export function setHostShown(hostId: string, shown: boolean): boolean {
  const host = localHostOf(useHostStore.getState().hosts, hostId)
  if (host === undefined) return false
  const owner = resolveShownOwnerNow()
  if (owner === null) return false
  if (owner.kind === 'master') {
    const store = useShownHostsStore.getState()
    if (shown) store.show(wireIdOfHost(host))
    else for (const form of shownFormsOf(host)) useShownHostsStore.getState().hide(form)
    return true
  }
  const wire = wireIdOfHost(host)
  const forms = shownFormsOf(host)
  const written = useLocalProfilesStore.getState().setSlaveShownHosts(owner.id, (ids) => {
    if (shown) return ids.includes(wire) ? ids : [...ids, wire]
    return ids.some((id) => forms.includes(id)) ? ids.filter((id) => !forms.includes(id)) : ids
  })
  return written.ok
}

// === Re-key (spec §4.3, plan §0.12; run by the re-resolve pass) ===

/**
 * The pass's shown-hosts step: a listed local id of a host whose daemonId is known becomes its `d1_…` id, in place;
 * when that `d1_…` is already listed the local id is dropped (`rekeyShownIds`). Same moves as the look re-key
 * (`wireKeyMovesOf` — a daemon two rows claim moves nothing). EVERY workbench's list (per-workbench shown hosts A5):
 * the master's (`useShownHostsStore`) and each local workbench's (`LocalProfile.shownHostIds`), by the same moves.
 * `null` when nothing would move in any of them; else the write and its way back, for the pass to commit under its
 * lock. Never part of the explicit-map rewrite a deletion reuses: a deletion re-keys no list.
 *
 * TWO STORES, ONE STEP. The slaves are read AT COMMIT, never planned here: the pass's 'parked worlds' write (earlier
 * in the same commit) sets `slaves` too, and a value planned from before it would put the old worlds back. The commit
 * remembers both stores as it found them, before its first write; the undo — `commitAll` calls it once, also when this
 * commit threw half-way — attempts BOTH restores even if the first throws, then throws one combined error (the pass's
 * `rollback-failed`). Undos run newest first, so the parked-worlds undo still restores the pass's pre-state after it.
 */
export function rekeyShownHosts(hosts: Hosts): { commit: () => void; undo: () => void } | null {
  const { ids } = useShownHostsStore.getState()
  const { slaves } = useLocalProfilesStore.getState()
  const listed = (id: string): boolean => ids.includes(id) || Object.values(slaves).some((s) => s.shownHostIds.includes(id))
  const moves = wireKeyMovesOf(hosts).filter(([from]) => listed(from))
  if (moves.length === 0) return null
  /** What the commit wrote and what it found: the master's list, and per slave its list. Filled BEFORE each write, so
   *  an undo after a write that threw half-way (memory set, storage refused) still knows. */
  let master: { before: string[]; wrote: string[] } | null = null
  const slaveBefore: Record<string, string[]> = {}
  const slaveWrote: Record<string, string[]> = {}
  return {
    // FIELD BY FIELD, ON WHAT STORAGE HOLDS NOW (PR-A2 review). Each store is first brought up to storage — a
    // synchronous rehydrate when its record differs from memory (`catchUpWithStorage`) — so a write another renderer
    // persisted after the pass began is the base, not overwritten; then only the lists are written: the master's
    // `ids`, and each slave's `shownHostIds` through `mapSlaveShownHosts` (no other field, no other record). What is
    // left is a write that lands INSIDE this synchronous block — the cross-renderer residual #1256 (no CAS on
    // localStorage), as for every store the pass writes.
    commit: () => {
      catchUpWithStorage(useShownHostsStore)
      const current = useShownHostsStore.getState().ids
      const next = rekeyShownIds(current, moves)
      if (next !== current) {
        master = { before: current, wrote: next }
        useShownHostsStore.setState({ ids: next })
      }
      catchUpWithStorage(useLocalProfilesStore)
      useLocalProfilesStore.getState().mapSlaveShownHosts((list, id) => {
        const moved = rekeyShownIds(list, moves)
        if (moved !== list) {
          slaveBefore[id] = list
          slaveWrote[id] = moved
        }
        return moved
      })
    },
    // Conditional, per list: put back only a list that is still exactly (the same array) what the commit wrote — a
    // list somebody changed since is theirs. Both stores are attempted even if the first throws; one combined error.
    undo: () => {
      const failed: string[] = []
      const m = master
      if (m !== null && useShownHostsStore.getState().ids === m.wrote) {
        try {
          useShownHostsStore.setState({ ids: m.before })
        } catch (err) {
          failed.push(`the master's list: ${err instanceof Error ? err.message : String(err)}`)
        }
      }
      if (Object.keys(slaveWrote).length > 0) {
        try {
          useLocalProfilesStore.getState().mapSlaveShownHosts((list, id) => (Object.hasOwn(slaveWrote, id) && list === slaveWrote[id] ? slaveBefore[id] : list))
        } catch (err) {
          failed.push(`the local workbenches' lists: ${err instanceof Error ? err.message : String(err)}`)
        }
      }
      if (failed.length > 0) throw new Error(`shown hosts undo incomplete — ${failed.join('; ')}`)
    },
  }
}

/** A persisted store as far as `catchUpWithStorage` needs it. */
interface Rereadable {
  getState: () => unknown
  persist: { getOptions: () => { name?: string; version?: number; partialize?: (state: never) => unknown }; rehydrate: () => unknown }
}

/**
 * `store` brought up to what storage holds NOW: rehydrated when its persisted record is not exactly what its memory
 * would write (the rule of host-reresolve.ts's `rereadFromStorage`). Synchronous — both stores persist to
 * localStorage, and zustand's rehydrate over a synchronous storage completes inside the call.
 */
function catchUpWithStorage(target: unknown): void {
  const store = target as Rereadable
  const { name, version, partialize } = store.persist.getOptions()
  if (name === undefined) return
  let raw: string | null
  try {
    raw = localStorage.getItem(name)
  } catch {
    return // unreadable storage: memory is all there is
  }
  if (raw === null) return
  const state = store.getState()
  if (raw !== JSON.stringify({ state: partialize ? partialize(state as never) : state, version })) void store.persist.rehydrate()
}
