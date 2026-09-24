// spa/src/lib/host-reresolve.ts — the host re-resolve pass (host ownership spec §3.3).
//
// A host reference this device cannot resolve is stored verbatim — the wire id, byte for byte (§3.2). When that host
// later arrives here (added, its daemonId learned, an alias learned, a conflict cleared), every such reference must
// point at the local host. This pass does that, over everything this device holds: the tab store on screen, every
// parked world, `purdex-host-settings` keys and the New Tab host-bearing columns (presets and knownIds). It also
// re-keys the stores keyed by WIRE id (H2c-2: `purdex-host-looks`; H2d-1: `purdex-shown-hosts`) the other way — local id → `d1_…` once a host's
// daemonId is known (spec §4.3) — the one step that changes a payload (`settings`, one push; plan §0.13).
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
// A fresh view, not a safe one (#1256): the operation lock is this renderer's only, and localStorage has no
// compare-and-set. So the body starts by re-reading, from storage, every store it rewrites (plus the workspace store,
// which with the tab and local-profiles stores decides whether the world is settled — `readMasterWorld`, epoch and
// fence) and plans from that; between that read and the writes nothing is awaited. What is left is the residual
// #1256 accepts for the world switch: another renderer's write this one does not see yet — a moment late — can still
// be overwritten by the pass's whole-store write. Closing it needs a commit point off localStorage, not this pass.
//
// A failed write is put back — not claimed atomic. The next state of every store is computed first, then written
// store by store; a write that throws (the persist's `setItem` — quota, SecurityError — after zustand already
// changed memory) puts back every store begun, the failing one included, newest first: `write-failed`, retried with
// backoff. When putting back fails too, the stores are left PARTLY rewritten — some at the target, some not, a
// store's memory possibly put back while its storage keeps the new value: `rollback-failed`, and the pass is run again
// at once (then backs off like any failure). That is how it converges: the pass recomputes its targets from what the
// stores hold (re-read from storage first) and is idempotent, so every run that gets its writes through rolls the
// state FORWARD to the target — and after a reload, the hydration pass does the same from whatever storage holds.
// No journal is kept: nothing but the rerun is needed to finish.

import { useHostStore } from '../stores/useHostStore'
import { rewriteTabsHosts, useTabStore } from '../stores/useTabStore'
import { useLocalProfilesStore } from '../stores/useLocalProfilesStore'
import type { LocalProfile, ParkedWorld } from '../stores/useLocalProfilesStore'
import { useHostSettingsStore } from '../stores/useHostSettingsStore'
import { renameLayoutIds, useNewTabLayoutStore } from '../stores/useNewTabLayoutStore'
import { useRebuildStore, type OperationLockGrant } from '../stores/useRebuildStore'
import { useHostLookStore } from '../stores/useHostLookStore'
import { useShownHostsStore } from '../stores/useShownHostsStore'
import { useWorkspaceStore } from '../features/workspace/store'
import { readMasterWorld } from './profile/master-world'
import { hostSettingsFromWire, presetColumnIdFromWire } from './profile/host-identity'
import { hostResolverSignature, wireResolverOf } from './profile/sections'
import { rekeyWireKeyedStores } from './host-look'
import { rekeyShownHosts } from './shown-hosts'

export const HOST_RERESOLVE_LOCK_OWNER = 'host-reresolve'
/** The retry interval while the lock is held elsewhere, and the first backoff step after a failed write. */
export const HOST_RERESOLVE_RETRY_MS = 500
/** The backoff after failed writes doubles up to this. */
export const HOST_RERESOLVE_MAX_RETRY_MS = 30_000

/**
 * `done` — ran, or had nothing to move; `conflict` — identity conflict, nothing done; `busy` — lock held elsewhere
 * (or the world unsettled); `write-failed` — a store write threw and every store begun was put back;
 * `rollback-failed` — putting back threw too: the stores may be partly rewritten until a rerun rolls them forward.
 */
export type HostReresolveOutcome = 'done' | 'conflict' | 'busy' | 'write-failed' | 'rollback-failed'

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
function commitAll(writes: readonly StoreWrite[]): 'ok' | 'write-failed' | 'rollback-failed' {
  const begun: StoreWrite[] = []
  try {
    for (const write of writes) {
      begun.push(write)
      write.commit()
    }
    return 'ok'
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
      console.error(`[host-reresolve] rollback incomplete after a failed write (${messageOf(err)}) — ${unfinished.join('; ')}; rerunning to roll forward`)
      return 'rollback-failed'
    }
    console.warn(`[host-reresolve] a store write failed and was rolled back: ${messageOf(err)}`)
    return 'write-failed'
  }
}

/**
 * The pass's core with an EXPLICIT map — a key of `map` is rewritten to its value, every other id is left alone —
 * over the same stores and with the same collision rules (plan §0.11). The host deletion's direction, local id →
 * wire id (host ownership spec §3.4; the reverse is the pass itself). Synchronous, and it takes no lock and re-reads
 * nothing: the caller decides both (the deletion is lock-free — plan §0.1). `ok` when nothing needed writing too.
 * Never throws.
 */
export function rewriteHostRefs(map: Readonly<Record<string, string>>): 'ok' | 'write-failed' | 'rollback-failed' {
  return commitAll(planRewrite((id) => (Object.hasOwn(map, id) ? map[id] : id)))
}

/** A persisted store as far as the re-read needs it. */
interface Rereadable {
  getState: () => unknown
  persist: {
    getOptions: () => { name?: string; version?: number; partialize?: (state: never) => unknown }
    rehydrate: () => unknown
  }
}

/** The stores the pass rewrites, and the workspace store the world's settledness is read with. */
const REREAD: readonly Rereadable[] = [useTabStore, useWorkspaceStore, useLocalProfilesStore, useHostSettingsStore, useNewTabLayoutStore, useHostLookStore, useShownHostsStore] as unknown as Rereadable[]

/**
 * Bring every store in `REREAD` up to what storage holds NOW. A store whose persisted record is exactly what its
 * memory would write is left alone (its objects keep their identity); any other is rehydrated — synchronously, as
 * every store here persists to localStorage (apply-to-stores' premises pin that).
 */
function rereadFromStorage(): void {
  for (const store of REREAD) {
    const { name, version, partialize } = store.persist.getOptions()
    if (name === undefined) continue
    let raw: string | null
    try {
      raw = localStorage.getItem(name)
    } catch {
      continue
    }
    if (raw === null) continue
    const state = store.getState()
    const mine = JSON.stringify({ state: partialize ? partialize(state as never) : state, version })
    if (raw !== mine) void store.persist.rehydrate()
  }
}

/** The host resolver signature the last pass that finished (`done` / `conflict`) ran against. */
let settledSignature: string | null = null

/** Inside a pass: a rehydrate the re-read causes fires the hydration triggers — they must not start another. */
let inPass = false

/** One pass, now. Never throws: a busy lock, a conflict and a failed write are outcomes. */
export function runHostReresolve(): HostReresolveOutcome {
  return guarded(() => passBody(null, null))
}

/**
 * The pass's body for ONE host that has just come back — the undo of a host deletion (spec §3.4, plan §0.6): every
 * reference that resolves to `hostId` (its wire id, whichever world holds it and however it got there — `d1_X` is X
 * on every device) moves to it, now, synchronously. Under `parent` — the caller's operation-lock grant (the hosts
 * apply rolling back) — it runs inside that grant; without one it takes the lock itself. Only this host's references
 * move: the hosts apply rolls back from a STAGED host list, and a reference resolved onto a row of that stage would
 * point at a host this device is about to drop; every other host is the plain pass's (`requestHostReresolve`).
 * `busy` (the lock held elsewhere, the world unsettled) and a failed write are the caller's to reschedule. Never throws.
 */
export function reresolveRestoredHost(hostId: string, parent: OperationLockGrant | null = null): HostReresolveOutcome {
  return guarded(() => passBody(hostId, parent))
}

function guarded(body: () => HostReresolveOutcome): HostReresolveOutcome {
  if (inPass) return 'busy'
  inPass = true
  try {
    return body()
  } finally {
    inPass = false
  }
}

/** `only` — the one host references may move onto (`reresolveRestoredHost`), or `null`: every local host (the pass). */
function passBody(only: string | null, parent: OperationLockGrant | null): HostReresolveOutcome {
  const { hosts, hostOrder } = useHostStore.getState()
  const signature = hostResolverSignature({ hosts, hostOrder })
  const resolve = wireResolverOf({ hosts, hostOrder })
  if (resolve === null) {
    if (only === null) settledSignature = signature
    return 'conflict'
  }
  // Only an id that is NOT a local host moves, and only onto a local host (onto `only`, when set).
  const map: HostMap = (id) => {
    if (Object.hasOwn(hosts, id)) return id
    const local = resolve(id)
    return Object.hasOwn(hosts, local) && (only === null || local === only) ? local : id
  }
  // Everything from here to the last write is synchronous (the #1256 narrowing above).
  rereadFromStorage()
  // A world another window is mid-way through switching is nobody's to write: retried like a held lock.
  if (!readMasterWorld().settled) return 'busy'
  const writes = planRewrite(map)
  // The look store is keyed by WIRE id, so its re-key runs the other way from `map` — a host's local id → its `d1_…`
  // (spec §4.3, plan §0.12) — computed from the hosts alone, as its own step: never inside `planRewrite`, the
  // explicit-map rewrite a deletion (local → wire) reuses and that must move no look (decision 9). It changes the
  // `settings` payload: one push (plan §0.13).
  const lookRekey = rekeyWireKeyedStores(hosts)
  if (lookRekey !== null) writes.push({ key: 'host looks', ...lookRekey })
  // The shown-hosts ids are WIRE ids too (H2d-1): same direction, same rule, its own step — never in `planRewrite`.
  const shownRekey = rekeyShownHosts(hosts)
  if (shownRekey !== null) writes.push({ key: 'shown hosts', ...shownRekey })
  if (writes.length > 0) {
    // Taking the lock is not free — every release reconciles every host's sessions — so only when something moves.
    const grant = useRebuildStore.getState().acquireOperationLock(HOST_RERESOLVE_LOCK_OWNER, parent)
    if (grant === null) return 'busy'
    try {
      const committed = commitAll(writes)
      if (committed !== 'ok') return committed
    } finally {
      useRebuildStore.getState().releaseOperationLock(grant)
    }
  }
  if (only === null) settledSignature = signature
  return 'done'
}

let retry: ReturnType<typeof setTimeout> | null = null
let failedWrites = 0
/** A `rollback-failed` pass has had its immediate rerun; until a pass succeeds, the next failure backs off. */
let rerunAtOnceUsed = false

function scheduleRetry(ms: number): void {
  retry = setTimeout(() => {
    retry = null
    requestHostReresolve()
  }, ms)
}

/**
 * Ask for a pass: it runs now. While the lock is held elsewhere it is retried every `HOST_RERESOLVE_RETRY_MS`; after
 * a failed write it is retried with a doubling backoff from `HOST_RERESOLVE_RETRY_MS` up to
 * `HOST_RERESOLVE_MAX_RETRY_MS` — except a first `rollback-failed`, rerun at once to roll the stores forward. A newer request supersedes a pending retry. Under a conflict nothing is scheduled:
 * the conflict clearing changes the identity, which requests again. Never throws.
 */
export function requestHostReresolve(): void {
  if (inPass) return // the pass under way re-reads every store before it plans
  cancelRetry()
  const outcome = runHostReresolve()
  if (outcome === 'busy') {
    scheduleRetry(HOST_RERESOLVE_RETRY_MS)
  } else if (outcome === 'rollback-failed' && !rerunAtOnceUsed) {
    // Partly rewritten: roll forward at once rather than leave it for a backoff.
    rerunAtOnceUsed = true
    scheduleRetry(0)
  } else if (outcome === 'write-failed' || outcome === 'rollback-failed') {
    failedWrites++
    scheduleRetry(Math.min(HOST_RERESOLVE_RETRY_MS * 2 ** (failedWrites - 1), HOST_RERESOLVE_MAX_RETRY_MS))
  } else {
    failedWrites = 0
    rerunAtOnceUsed = false
  }
}

/**
 * A request for a pass that cannot throw and runs nothing now: the pass itself runs in a microtask, and whatever it
 * throws is caught and reported there. For callers that must not have their own outcome or error replaced — the
 * profile applies' `finally` (`applySectionToStores`).
 */
export function scheduleHostReresolve(): void {
  try {
    queueMicrotask(() => {
      try {
        requestHostReresolve()
      } catch (err) {
        console.error(`[host-reresolve] the pass threw: ${messageOf(err)}`)
      }
    })
  } catch {
    // no microtask queue: nothing to schedule on, and nothing here may throw
  }
}

/** Every persisted store the pass reads or rewrites: it runs only once ALL of them hold their real state. */
const STORES = [useHostStore, useTabStore, useNewTabLayoutStore, useLocalProfilesStore, useHostSettingsStore, useHostLookStore, useShownHostsStore] as const

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
  rerunAtOnceUsed = false
  settledSignature = null
}
