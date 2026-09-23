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
// cascade take to rewrite the tab tree — and only when something moves (every release of that lock reconciles every
// host's session list). The body is one synchronous stretch: nothing can interleave with it in this window.
// Refused → retried later; a newer request supersedes a pending retry.
//
// All or nothing: the next state of every store is computed first, then written store by store; a write that throws
// (the persist's `setItem` — quota, SecurityError — after zustand already changed memory) puts back every store
// written so far, the failing one included, and the pass answers `write-failed` and is retried with backoff.
import { useHostStore } from '../stores/useHostStore'
import { rewriteTabsHosts, useTabStore } from '../stores/useTabStore'
import { useLocalProfilesStore } from '../stores/useLocalProfilesStore'
import type { LocalProfile, ParkedWorld } from '../stores/useLocalProfilesStore'
import { useHostSettingsStore } from '../stores/useHostSettingsStore'
import { renameLayoutIds, useNewTabLayoutStore } from '../stores/useNewTabLayoutStore'
import { useRebuildStore } from '../stores/useRebuildStore'
import { hostSettingsFromWire, presetColumnIdFromWire } from './profile/host-identity'
import { hostResolverSignature, wireResolverOf } from './profile/sections'

export const HOST_RERESOLVE_LOCK_OWNER = 'host-reresolve'
/** The retry interval while the lock is held elsewhere, and the first backoff step after a failed write. */
export const HOST_RERESOLVE_RETRY_MS = 500
/** The backoff after failed writes doubles up to this. */
export const HOST_RERESOLVE_MAX_RETRY_MS = 30_000

/**
 * `done` — ran, or had nothing to move; `conflict` — identity conflict, nothing done; `busy` — lock held elsewhere;
 * `write-failed` — a store write threw, every store is back as it was.
 */
export type HostReresolveOutcome = 'done' | 'conflict' | 'busy' | 'write-failed'

type HostMap = (hostId: string) => string

/** One store's step of the rewrite: its next state, and the way back to the state it was computed from. */
interface StoreWrite {
  key: string
  commit: () => void
  undo: () => void
}

function mapWorld(world: ParkedWorld, map: HostMap): ParkedWorld {
  const tabs = rewriteTabsHosts(world.tabs, map)
  return tabs === world.tabs ? world : { ...world, tabs }
}

const columnMap = (map: HostMap) => (id: string) => presetColumnIdFromWire(id, map)

/**
 * Every host reference this device holds, rewritten through `map` — on screen, in every parked world, the
 * host-settings keys (a sync-id entry wins a collision — `rekeyEntries`) and the New Tab columns (first occurrence
 * wins) — as one write per store that moves. Empty when nothing moves. Reads only.
 */
function planRewrite(map: HostMap): StoreWrite[] {
  const writes: StoreWrite[] = []

  const tabs = useTabStore.getState().tabs
  const tabsNext = rewriteTabsHosts(tabs, map)
  if (tabsNext !== tabs) {
    writes.push({ key: 'tabs', commit: () => useTabStore.setState({ tabs: tabsNext }), undo: () => useTabStore.setState({ tabs }) })
  }

  const { parkedMaster, slaves } = useLocalProfilesStore.getState()
  const masterNext = parkedMaster === null ? null : mapWorld(parkedMaster, map)
  let slavesNext: Record<string, LocalProfile> = slaves
  for (const [id, slave] of Object.entries(slaves)) {
    const world = slave.world === null ? null : mapWorld(slave.world, map)
    if (world === slave.world) continue
    if (slavesNext === slaves) slavesNext = { ...slaves }
    slavesNext[id] = { ...slave, world }
  }
  if (masterNext !== parkedMaster || slavesNext !== slaves) {
    writes.push({
      key: 'parked worlds',
      commit: () => useLocalProfilesStore.setState({ parkedMaster: masterNext, slaves: slavesNext }),
      undo: () => useLocalProfilesStore.setState({ parkedMaster, slaves }),
    })
  }

  const settings = useHostSettingsStore.getState().hosts
  if (Object.keys(settings).some((id) => map(id) !== id)) {
    const settingsNext = hostSettingsFromWire(settings, map)
    writes.push({ key: 'host settings', commit: () => useHostSettingsStore.setState({ hosts: settingsNext }), undo: () => useHostSettingsStore.setState({ hosts: settings }) })
  }

  const layout = useNewTabLayoutStore.getState()
  const layoutNext = renameLayoutIds(layout, columnMap(map))
  if (layoutNext !== null) {
    const { presets, knownIds } = layout
    writes.push({ key: 'new-tab layout', commit: () => useNewTabLayoutStore.setState(layoutNext), undo: () => useNewTabLayoutStore.setState({ presets, knownIds }) })
  }
  return writes
}

const messageOf = (err: unknown): string => (err instanceof Error ? err.message : String(err))

/** Writes every step; on a throw, undoes every step begun — the failing one too — newest first. Never throws. */
function commitAll(writes: readonly StoreWrite[]): boolean {
  const begun: StoreWrite[] = []
  try {
    for (const write of writes) {
      begun.push(write)
      write.commit()
    }
    return true
  } catch (err) {
    const unfinished: string[] = []
    for (const write of begun.reverse()) {
      try {
        write.undo()
      } catch (undoErr) {
        unfinished.push(`${write.key}: ${messageOf(undoErr)}`)
      }
    }
    if (unfinished.length > 0) {
      console.error(`[host-reresolve] rollback incomplete after a failed write (${messageOf(err)}) — ${unfinished.join('; ')}`)
    } else {
      console.warn(`[host-reresolve] a store write failed and was rolled back: ${messageOf(err)}`)
    }
    return false
  }
}

/** The host resolver signature the last pass that finished (`done` / `conflict`) ran against. */
let settledSignature: string | null = null

/** One pass, now. Never throws: a busy lock, a conflict and a failed write are outcomes. */
export function runHostReresolve(): HostReresolveOutcome {
  const { hosts, hostOrder } = useHostStore.getState()
  const signature = hostResolverSignature({ hosts, hostOrder })
  const resolve = wireResolverOf({ hosts, hostOrder })
  if (resolve === null) {
    settledSignature = signature
    return 'conflict'
  }
  // Only an id that is NOT a local host moves, and only onto a local host.
  const map: HostMap = (id) => {
    if (Object.hasOwn(hosts, id)) return id
    const local = resolve(id)
    return Object.hasOwn(hosts, local) ? local : id
  }
  const writes = planRewrite(map)
  if (writes.length > 0) {
    // Taking the lock is not free — every release reconciles every host's sessions — so only when something moves.
    const grant = useRebuildStore.getState().acquireOperationLock(HOST_RERESOLVE_LOCK_OWNER)
    if (grant === null) return 'busy'
    try {
      if (!commitAll(writes)) return 'write-failed'
    } finally {
      useRebuildStore.getState().releaseOperationLock(grant)
    }
  }
  settledSignature = signature
  return 'done'
}

let retry: ReturnType<typeof setTimeout> | null = null
let failedWrites = 0

function scheduleRetry(ms: number): void {
  retry = setTimeout(() => {
    retry = null
    requestHostReresolve()
  }, ms)
}

/**
 * Ask for a pass: it runs now. While the lock is held elsewhere it is retried every `HOST_RERESOLVE_RETRY_MS`; after
 * a failed write it is retried with a doubling backoff from `HOST_RERESOLVE_RETRY_MS` up to
 * `HOST_RERESOLVE_MAX_RETRY_MS`. A newer request supersedes a pending retry. Under a conflict nothing is scheduled:
 * the conflict clearing changes the identity, which requests again. Never throws.
 */
export function requestHostReresolve(): void {
  cancelRetry()
  const outcome = runHostReresolve()
  if (outcome === 'busy') {
    scheduleRetry(HOST_RERESOLVE_RETRY_MS)
  } else if (outcome === 'write-failed') {
    failedWrites++
    scheduleRetry(Math.min(HOST_RERESOLVE_RETRY_MS * 2 ** (failedWrites - 1), HOST_RERESOLVE_MAX_RETRY_MS))
  } else {
    failedWrites = 0
  }
}

/** Every persisted store the pass reads or rewrites: it runs only once ALL of them hold their real state. */
const STORES = [useHostStore, useTabStore, useNewTabLayoutStore, useLocalProfilesStore, useHostSettingsStore] as const

/**
 * The pass's triggers, for the app's lifetime (`main.tsx`): once every store it touches has hydrated; again whenever
 * one of them finishes a (re)hydration — a store landing after the first pass, or one another window rewrote, may hold
 * a wire id this device can resolve; and on every host-store change whose resolver signature (a host added / removed,
 * a daemonId learned or cleared, a conflict entered or left, `syncAliases` changed — not a rename, not runtime churn)
 * differs from the one the last FINISHED pass ran against — an identity whose pass failed is not handled yet.
 * The profile applies request one too, when they settle (`applySectionToStores`).
 */
export function startHostReresolve(): () => void {
  const request = () => {
    if (STORES.every((store) => store.persist.hasHydrated())) requestHostReresolve()
  }
  const unsubs = STORES.map((store) => store.persist.onFinishHydration(request))
  unsubs.push(
    useHostStore.subscribe((state, prev) => {
      if (state.hosts === prev.hosts && state.hostOrder === prev.hostOrder) return // runtime / active-host churn
      if (hostResolverSignature(state) === settledSignature) return
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
  failedWrites = 0
  settledSignature = null
}
