// spa/src/lib/host-reresolve.ts — the host re-resolve pass (host ownership spec §3.3).
//
// A host reference this device cannot resolve is stored verbatim — the wire id, byte for byte (§3.2). When that host
// later arrives here (added, its daemonId learned, an alias learned, a conflict cleared), every such reference must
// point at the local host. This pass does that, over everything this device holds: the tab store on screen, every
// parked world, `purdex-host-settings` keys and the New Tab host-bearing columns (presets and knownIds).
//
// No push follows: for a `d1_…` reference the build maps the new local id back to the same `d1_…` (§3.3 no-push
// invariant). Only a reference resolved through a legacy alias is canonicalised by the next build — one push, as a
// pull of it already does.
//
// Lock: the in-process OPERATION lock, owner `host-reresolve` (plan §0.1) — what the tabs apply and the hosts
// cascade take to rewrite the tab tree. The body is one synchronous stretch: nothing can interleave with it in this
// window. Refused → retried later; a newer request supersedes a pending retry.
import { useHostStore } from '../stores/useHostStore'
import { useTabStore } from '../stores/useTabStore'
import { useLocalProfilesStore } from '../stores/useLocalProfilesStore'
import type { ParkedWorld } from '../stores/useLocalProfilesStore'
import { useHostSettingsStore } from '../stores/useHostSettingsStore'
import { useNewTabLayoutStore } from '../stores/useNewTabLayoutStore'
import { useRebuildStore } from '../stores/useRebuildStore'
import { hostSettingsFromWire, layoutFromWire, presetColumnIdFromWire } from './profile/host-identity'
import { hostResolverSignature, wireResolverOf } from './profile/sections'
import type { Tab } from '../types/tab'

export const HOST_RERESOLVE_LOCK_OWNER = 'host-reresolve'
export const HOST_RERESOLVE_RETRY_MS = 500

/** `done` — ran, or had nothing to move; `conflict` — identity conflict, nothing done; `busy` — lock held elsewhere. */
export type HostReresolveOutcome = 'done' | 'conflict' | 'busy'

type HostMap = (hostId: string) => string

function mapWorld(world: ParkedWorld, map: HostMap): ParkedWorld {
  let tabs: Record<string, Tab> | null = null
  for (const [id, tab] of Object.entries(world.tabs)) {
    const layout = layoutFromWire(tab.layout, map)
    if (layout === tab.layout) continue
    tabs ??= { ...world.tabs }
    tabs[id] = { ...tab, layout }
  }
  return tabs === null ? world : { ...world, tabs }
}

/**
 * Rewrite every host reference this device holds through `map`: on screen, in every parked world, the host-settings
 * keys (a sync-id entry wins a collision — `rekeyEntries`) and the New Tab columns (first occurrence wins). A store
 * nothing moves in is not written. Synchronous; the caller holds the operation lock.
 */
function rewriteHostRefs(map: HostMap): void {
  useTabStore.getState().rewritePaneHosts(map)
  useLocalProfilesStore.getState().updateParkedWorlds((world) => mapWorld(world, map))
  const settings = useHostSettingsStore.getState().hosts
  if (Object.keys(settings).some((id) => map(id) !== id)) {
    useHostSettingsStore.setState({ hosts: hostSettingsFromWire(settings, map) })
  }
  useNewTabLayoutStore.getState().renameIds(columnMap(map))
}

const columnMap = (map: HostMap) => (id: string) => presetColumnIdFromWire(id, map)

/**
 * Whether `map` moves any reference this device holds. Checked BEFORE the lock is taken, in the same synchronous
 * stretch as the write: taking and releasing the operation lock is not free — every release reconciles the session
 * lists of every host (`createOperationLockObserver`) — so a pass with nothing to do must not touch it.
 */
function anythingMoves(map: HostMap): boolean {
  const moves = (tabs: Record<string, Tab>) => Object.values(tabs).some((tab) => layoutFromWire(tab.layout, map) !== tab.layout)
  if (moves(useTabStore.getState().tabs)) return true
  const { parkedMaster, slaves } = useLocalProfilesStore.getState()
  if (parkedMaster !== null && moves(parkedMaster.tabs)) return true
  if (Object.values(slaves).some((slave) => slave.world !== null && moves(slave.world.tabs))) return true
  if (Object.keys(useHostSettingsStore.getState().hosts).some((id) => map(id) !== id)) return true
  const { presets, knownIds } = useNewTabLayoutStore.getState()
  const col = columnMap(map)
  return [...knownIds, ...Object.values(presets).flatMap((preset) => preset.columns.flat())].some((id) => col(id) !== id)
}

/** One pass, now. Never throws on a busy lock or a conflict — it says so. */
export function runHostReresolve(): HostReresolveOutcome {
  const { hosts, hostOrder } = useHostStore.getState()
  const resolve = wireResolverOf({ hosts, hostOrder })
  if (resolve === null) return 'conflict'
  // Only an id that is NOT a local host moves, and only onto a local host.
  const map: HostMap = (id) => {
    if (Object.hasOwn(hosts, id)) return id
    const local = resolve(id)
    return Object.hasOwn(hosts, local) ? local : id
  }
  if (!anythingMoves(map)) return 'done'
  const grant = useRebuildStore.getState().acquireOperationLock(HOST_RERESOLVE_LOCK_OWNER)
  if (grant === null) return 'busy'
  try {
    rewriteHostRefs(map)
  } finally {
    useRebuildStore.getState().releaseOperationLock(grant)
  }
  return 'done'
}

let retry: ReturnType<typeof setTimeout> | null = null

/**
 * Ask for a pass: it runs now, and while the lock is held elsewhere it is retried every
 * `HOST_RERESOLVE_RETRY_MS` until it runs. A newer request supersedes a pending retry.
 * Under a conflict nothing is scheduled: the conflict clearing changes the identity, which requests again.
 */
export function requestHostReresolve(): void {
  cancelRetry()
  if (runHostReresolve() !== 'busy') return
  retry = setTimeout(() => {
    retry = null
    requestHostReresolve()
  }, HOST_RERESOLVE_RETRY_MS)
}

/** Every persisted store the pass reads or rewrites: it runs only once ALL of them hold their real state. */
const STORES = [useHostStore, useTabStore, useNewTabLayoutStore, useLocalProfilesStore, useHostSettingsStore] as const

/**
 * The pass's triggers, for the app's lifetime (`main.tsx`): once every store it touches has hydrated; again whenever
 * one of them finishes a (re)hydration — a store landing after the first pass, or one another window rewrote, may hold
 * a wire id this device can resolve; and on every change of the host resolver signature (a host added / removed, a
 * daemonId learned or cleared, a conflict entered or left, `syncAliases` changed — not a rename, not runtime churn).
 * The profile applies request one too, when they settle (`applySectionToStores`).
 */
export function startHostReresolve(): () => void {
  const request = () => {
    if (STORES.every((store) => store.persist.hasHydrated())) requestHostReresolve()
  }
  const unsubs = STORES.map((store) => store.persist.onFinishHydration(request))
  let seen = hostResolverSignature(useHostStore.getState())
  unsubs.push(
    useHostStore.subscribe((state, prev) => {
      if (state.hosts === prev.hosts && state.hostOrder === prev.hostOrder) return // runtime / active-host churn
      const signature = hostResolverSignature(state)
      if (signature === seen) return
      seen = signature
      request()
    }),
  )
  request()
  return () => {
    for (const unsub of unsubs) unsub()
    cancelRetry()
  }
}

function cancelRetry(): void {
  if (retry !== null) clearTimeout(retry)
  retry = null
}

export function __resetHostReresolveForTest(): void {
  cancelRetry()
}
