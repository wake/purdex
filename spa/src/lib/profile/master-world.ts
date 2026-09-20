// spa/src/lib/profile/master-world.ts — where THE MASTER PROFILE'S tab world is
// on this device — or that nobody can say yet (Profile Sync spec §4.1, §9.12; P3
// plan, P3b Task 4).
//
// The pure core never assumes that what the stores hold is what the profile
// owns. This file is the ONE door the impure halves (collector.ts,
// apply-to-stores.ts, executor.ts) go through, because since local profiles
// ("slaves") exist the screen can show a world that must never reach the SOT,
// while the master's world is parked in `useLocalProfilesStore.parkedMaster` —
// and keeps syncing there (decision 9).
//
// WHY "ON SCREEN OR PARKED" IS NOT ENOUGH: THE EPOCH BARRIER. A switch is one
// synchronous block in the window that makes it. Every OTHER window learns of
// it by rehydrating three persisted stores — tabs, workspaces, local profiles —
// one by one, in no guaranteed order. A leader in such a window could see the
// slave's tabs in the live stores while the pointer still says "master". So the
// two live stores carry a TAG (`worldId`, `worldEpoch`), a switch stamps the
// same pair into all three stores, and the master's world is readable only
// while they agree:
//
//   settled ⇔ the three `worldEpoch`s are equal
//           ∧ the two live stores carry the same `worldId`
//           ∧ that id is `useLocalProfilesStore.activeProfileId`
//           ∧ that epoch is not BEHIND THE FENCE (lib/storage/world-fence.ts)
//
// The last line is the window to which NOTHING of another window's switch or
// promote has arrived yet: its three stores agree with each other — on a world
// the device has already replaced. Agreement in memory cannot tell; the side key
// the other window raised FIRST, read from `localStorage`, can (`behind-fence`).
// Such a window must not lead a sync (it would push, or pull into, a world that
// is no longer the master's — after a promote, under the very label "master"),
// must not switch (it would park an outdated screen over the current world) and
// must not promote. One synchronous read of a tiny key per look; absent for a
// user who never switched, and then it is 0 and no epoch is behind it.
//
// Unsettled is an answer, not an error: the collector reports NOTHING of the
// tab world, an apply answers `busy`, the executor neither pulls, pushes nor
// sweeps a `tabs.*`. A rehydrate that never arrives leaves sync silent — silent,
// never wrong. A tag that is not a string / an epoch that is not a safe integer
// (storage junk: the two live stores have no `merge` to sanitise it, on purpose —
// see `commitTabWorld`) simply never agrees with anything.
//
// NOTHING HERE REPAIRS. One unsettled state is permanent: Task 3's `merge` falls
// back to "master on screen" when a slave is on screen with no parked master,
// while the live stores keep the slave's tag. Re-tagging the screen as the
// master's would push a slave's world to the SOT. It is made VISIBLE instead
// (`masterWorldStuck`), and this file starts no timer to do so: THE IRON RULE
// (start.ts) — a user without a master pays for nothing — is kept by having no
// listener and no timer of its own; `subscribeMasterWorld` costs three
// subscriptions and only the collector, which exists only under a master, asks.
//
// …EXCEPT ONE REHYDRATE (`recoverUnsettledWorld`). A BroadcastChannel message is
// not guaranteed to arrive, and a window that missed one of the three would stay
// `epoch-mismatch` until its next reload although storage holds a perfectly
// settled world. So whoever LOOKS at an unsettled world — the collector's
// subscription, `masterWorldStuck`, a refused switch — asks the three stores to
// read storage again: only for a mismatch of epoch or world id, or a window
// behind the fence (a missing parked master is not in storage either), a
// microtask later and only if it is STILL
// unsettled then (a window's own switch is unsettled for the length of its
// synchronous block, and must not pay for a rehydrate), and ONCE per unsettled
// stretch — a rehydrate notifies the very subscribers that ask, so "once" is
// what keeps this from looping. That "once" is for the BACKGROUND (the
// collector, `masterWorldStuck`). A refusal handed to the USER — a switch, a
// promote, a copy — asks again every time: one call, one rehydrate, nothing to
// loop; and the background's one try can be spent too early (seen on real
// hardware: the fence visible to this process, the three stores not yet — the
// rehydrate read the old world, and nothing ever asked again). No timer. It does not contradict `commitTabWorld`
// ("no rehydrate here"): that is about the apply path, where a rehydrate per
// write would rebuild every tab object for a change to one; this is a one-off for
// a window whose screen is wrong anyway. It mislabels nothing: memory becomes what
// storage holds, tags and content together. The disk side of the same accident
// is lib/storage/world-fence.ts.
import { useWorkspaceStore } from '../../features/workspace/store'
import { MASTER_PROFILE_ID, useLocalProfilesStore } from '../../stores/useLocalProfilesStore'
import type { ParkedWorld } from '../../stores/useLocalProfilesStore'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceSettingsStore } from '../../stores/useWorkspaceSettingsStore'
import { isWorldEpoch, readWorldEpochFence } from '../storage/world-fence'
import type { Tab, Workspace } from '../../types/tab'
import { deriveTabOrder } from './applier'
import { isSyncableWorkspaceId } from './sections'

// === Reading ===

export type MasterWorld = ParkedWorld

export type UnsettledReason = 'epoch-mismatch' | 'world-mismatch' | 'behind-fence' | 'junk-epoch' | 'no-parked-master'

export type MasterWorldRead =
  /** `onScreen`: the world IS the live stores' (same references); else it is `parkedMaster`'s. */
  | { settled: true; onScreen: boolean; world: MasterWorld }
  | { settled: false; reason: UnsettledReason }

const unsettled = (reason: UnsettledReason): MasterWorldRead => ({ settled: false, reason })

export function readMasterWorld(): MasterWorldRead {
  const tab = useTabStore.getState()
  const ws = useWorkspaceStore.getState()
  const local = useLocalProfilesStore.getState()

  if (!isWorldEpoch(tab.worldEpoch) || tab.worldEpoch !== ws.worldEpoch || ws.worldEpoch !== local.worldEpoch) {
    return unsettled(isJunkEpochOnly(tab, ws, local) ? 'junk-epoch' : 'epoch-mismatch')
  }
  if (typeof tab.worldId !== 'string' || tab.worldId !== ws.worldId || ws.worldId !== local.activeProfileId) return unsettled('world-mismatch')
  if (local.worldEpoch < readWorldEpochFence()) return unsettled('behind-fence')

  if (local.activeProfileId === MASTER_PROFILE_ID) {
    return { settled: true, onScreen: true, world: { workspaces: ws.workspaces, tabs: tab.tabs, activeWorkspaceId: ws.activeWorkspaceId, activeTabId: tab.activeTabId } }
  }
  // Task 3's `merge` never lets this through from storage; an in-memory write could.
  if (local.parkedMaster === null) return unsettled('no-parked-master')
  return { settled: true, onScreen: false, world: local.parkedMaster }
}

/**
 * `junk-epoch`: the epochs disagree ONLY because a live store holds one that is no
 * epoch at all (a string, a NaN, beyond the ceiling — the two live stores have no
 * `merge`), while everything that can be trusted agrees: the epochs that ARE
 * epochs are one value and not behind the fence, and the world ids are one id,
 * the pointer's. Still unsettled — nothing is reported, applied or promoted — but
 * it names the one unsettled state in which whose world the screen holds is not
 * in doubt, and switch-active.ts lets a SWITCH through it when storage holds the
 * same junk (then no rehydrate will ever help, and the switch stamps a real
 * epoch into all three stores). Without that way out a corrupt epoch is for ever.
 */
function isJunkEpochOnly(tab: { worldId: unknown; worldEpoch: unknown }, ws: { worldId: unknown; worldEpoch: unknown }, local: { activeProfileId: string; worldEpoch: number }): boolean {
  const real = [tab.worldEpoch, ws.worldEpoch, local.worldEpoch].filter(isWorldEpoch)
  if (real.length === 3 || real.some((e) => e !== real[0] || e < readWorldEpochFence())) return false
  return typeof tab.worldId === 'string' && tab.worldId === ws.worldId && ws.worldId === local.activeProfileId
}

/**
 * The ids of the workspaces the master profile's `workspaces` section holds on
 * this device — minus the ids that cannot form a `tabs.<id>` key, which
 * `buildWorkspacesSection` leaves out. Such a workspace is device-local as a
 * whole, its scoped settings included: sent, they would be an orphan to every
 * other client, whose corrective push would then come back and delete them HERE.
 *
 * `null` WHILE UNSETTLED — and never an empty set, which `buildSettingsSection`
 * would read as "the master has no workspace": every workspace-scoped entry
 * filtered out, a new hash, a gutted `settings` pushed to the SOT. The type makes
 * every caller say what it does instead (the collector: build nothing; an apply:
 * `busy`).
 */
export function masterWorkspaceIds(): ReadonlySet<string> | null {
  const read = readMasterWorld()
  return read.settled ? new Set(read.world.workspaces.map((ws) => ws.id).filter(isSyncableWorkspaceId)) : null
}

// === Writing ===

/** What a write of the tab world names. `activeTabId` is optional: see `repointActiveTab`. */
export interface TabWorld {
  tabs: Record<string, Tab>
  workspaces: Workspace[]
  activeWorkspaceId: string | null
  /** The world's OWN active tab — a world coming on screen brings one (Task 5). An apply leaves it out: the active
   *  tab is then whatever it was, while it survives. */
  activeTabId?: string | null
}

export interface WorldStamp {
  worldId: string
  worldEpoch: number
  /** Runs first when the write is rolled back. The caller lowers the epoch fence here (lib/storage/world-fence.ts):
   *  the restore writes carry the OLD epoch, and behind a fence still raised they would never reach storage. */
  beforeRollback?: () => void
}

/**
 * The active tab of `world`: the first of `preferred` whose tab survives, else
 * the active workspace's, else `null`. One rule for a world on screen and a
 * parked one.
 */
export function repointActiveTab(world: Pick<TabWorld, 'tabs' | 'workspaces' | 'activeWorkspaceId'>, ...preferred: (string | null | undefined)[]): string | null {
  const exists = (id: string | null | undefined): id is string => typeof id === 'string' && Object.hasOwn(world.tabs, id)
  const kept = preferred.find(exists)
  if (kept !== undefined) return kept
  const fallback = world.workspaces.find((w) => w.id === world.activeWorkspaceId)?.activeTabId
  return exists(fallback) ? fallback : null
}

/** What this file needs of a persisted zustand store — each store's state type is private to it. */
interface WritableStore {
  setState: (patch: Record<string, unknown>) => void
}

/** Best-effort restore of fields captured before a write; a rollback that throws must not stop the next one. */
function restore(store: WritableStore, old: Record<string, unknown>): void {
  try {
    store.setState(old)
  } catch {
    // the in-memory state is restored before persist's storage write can throw
  }
}

/**
 * Writes the tab store and the workspace store as one unit — either both hold the
 * new world or both hold the old one (`replaceTabSnapshot` is the precedent) —
 * keeping `useTabStore`'s four fields consistent: `tabOrder` re-derived,
 * `visitHistory` restricted to surviving tabs, the active tab re-pointed
 * (`repointActiveTab`: the world's own, else the one on screen, else the active
 * workspace's). `afterWrite` runs inside the same try, and what it touches — the
 * scoped workspace settings a removal clears — is part of the same snapshot: if
 * anything throws, all THREE stores go back. (A clear that landed before a later
 * one threw would otherwise be lost for good while its workspace came back.)
 *
 * `stamp` — the world CHANGES HANDS (a switch, a promote: Task 5): the tag goes
 * into both stores in the same two `setState`s as the content, so no state with
 * new tabs under an old tag is ever persisted, and a rollback takes it back too.
 * Without it the tag is not touched: an apply, like every ordinary edit, writes
 * INTO a world. (The caller writes the same epoch to `useLocalProfilesStore`.)
 *
 * No rehydrate here, on purpose. Neither store has a `merge` or an
 * `onRehydrateStorage` (the tab store's `migrate` does not run on a same-version
 * read), so there is no hook to run — while a rehydrate rebuilds the WHOLE state
 * from JSON, giving every tab and workspace a new identity and re-rendering
 * every pane for a change to one of them. Synchronous, so nothing interleaves.
 */
export function commitTabWorld(world: TabWorld, afterWrite?: () => void, stamp?: WorldStamp): void {
  const tabState = useTabStore.getState()
  const wsState = useWorkspaceStore.getState()
  const oldTab = { tabs: tabState.tabs, tabOrder: tabState.tabOrder, activeTabId: tabState.activeTabId, visitHistory: tabState.visitHistory, worldId: tabState.worldId, worldEpoch: tabState.worldEpoch }
  const oldWs = { workspaces: wsState.workspaces, activeWorkspaceId: wsState.activeWorkspaceId, worldId: wsState.worldId, worldEpoch: wsState.worldEpoch }
  const oldScoped = { workspaces: useWorkspaceSettingsStore.getState().workspaces }

  const tag = stamp === undefined ? {} : { worldId: stamp.worldId, worldEpoch: stamp.worldEpoch }
  const tabStore = useTabStore as unknown as WritableStore
  const wsStore = useWorkspaceStore as unknown as WritableStore
  try {
    tabStore.setState({
      tabs: world.tabs,
      tabOrder: deriveTabOrder(world.workspaces, world.tabs, tabState.tabOrder),
      activeTabId: repointActiveTab(world, world.activeTabId, tabState.activeTabId),
      visitHistory: tabState.visitHistory.filter((id) => Object.hasOwn(world.tabs, id)),
      ...tag,
    })
    wsStore.setState({ workspaces: world.workspaces, activeWorkspaceId: world.activeWorkspaceId, ...tag })
    afterWrite?.()
  } catch (err) {
    stamp?.beforeRollback?.()
    restore(tabStore, oldTab)
    restore(wsStore, oldWs)
    if (useWorkspaceSettingsStore.getState().workspaces !== oldScoped.workspaces) restore(useWorkspaceSettingsStore as unknown as WritableStore, oldScoped)
    throw err
  }
}

/**
 * The world on screen CHANGES HANDS and its content does not — a promote (Task 5)
 * relabels what the screen holds, or only moves the epoch under it. Writes the
 * tag, and nothing but the tag, into both live stores: not one tab or workspace
 * gets a new identity, so no pane re-renders and no terminal re-attaches. Both
 * or neither, like `commitTabWorld`; the caller has written — and on a throw
 * takes back — the same epoch in `useLocalProfilesStore`.
 */
export function restampWorld(stamp: WorldStamp): void {
  const tabState = useTabStore.getState()
  const wsState = useWorkspaceStore.getState()
  const oldTab = { worldId: tabState.worldId, worldEpoch: tabState.worldEpoch }
  const oldWs = { worldId: wsState.worldId, worldEpoch: wsState.worldEpoch }
  const tag = { worldId: stamp.worldId, worldEpoch: stamp.worldEpoch }
  const tabStore = useTabStore as unknown as WritableStore
  const wsStore = useWorkspaceStore as unknown as WritableStore
  try {
    tabStore.setState(tag)
    wsStore.setState(tag)
  } catch (err) {
    stamp.beforeRollback?.()
    restore(tabStore, oldTab)
    restore(wsStore, oldWs)
    throw err
  }
}

/**
 * Replaces the master's tab world, wherever it is. On screen: `commitTabWorld`.
 * Parked: `replaceParkedWorld('master', …)` — the live stores and the epoch do
 * not move, and the parked world's active tab is re-pointed by the same rule.
 * `afterWrite` runs on BOTH paths: what it does today is clear the scoped
 * settings of a workspace the SOT removed, and `useWorkspaceSettingsStore` is a
 * live store whatever is on screen. Same all-or-nothing: a throw puts the parked
 * world and the scoped settings back.
 *
 * `'unsettled'` = nothing was written and `afterWrite` did not run. Throws when
 * a store write throws, or when `next` is not a world the parking lot accepts.
 */
export function writeMasterWorld(next: TabWorld, afterWrite?: () => void): 'ok' | 'unsettled' {
  const read = readMasterWorld()
  if (!read.settled) return 'unsettled'
  if (read.onScreen) {
    commitTabWorld(next, afterWrite)
    return 'ok'
  }

  const old = read.world
  const oldScoped = { workspaces: useWorkspaceSettingsStore.getState().workspaces }
  const world: ParkedWorld = {
    tabs: next.tabs,
    workspaces: next.workspaces,
    activeWorkspaceId: next.activeWorkspaceId,
    activeTabId: repointActiveTab(next, next.activeTabId, old.activeTabId),
  }
  const written = useLocalProfilesStore.getState().replaceParkedWorld(MASTER_PROFILE_ID, world)
  if (!written.ok) throw new Error(`the parked master world was not written: ${written.reason}`)
  try {
    afterWrite?.()
  } catch (err) {
    try {
      useLocalProfilesStore.getState().replaceParkedWorld(MASTER_PROFILE_ID, old)
    } catch {
      // as `restore`: memory is back before persist's storage write can throw
    }
    if (useWorkspaceSettingsStore.getState().workspaces !== oldScoped.workspaces) restore(useWorkspaceSettingsStore as unknown as WritableStore, oldScoped)
    throw err
  }
  return 'ok'
}

// === Subscribing ===

/** Everything a reader of the master world can tell apart, by reference. */
function signature(read: MasterWorldRead): readonly unknown[] {
  return read.settled ? [true, read.onScreen, read.world.workspaces, read.world.tabs, read.world.activeWorkspaceId, read.world.activeTabId] : [false, read.reason]
}

/**
 * `fn` runs when the master world — its content, where it is, or whether anybody
 * can say — may have changed: after a change of any of the three stores that
 * moved one of those. An edit of a SLAVE on screen moves none of them (the world
 * is `parkedMaster`, untouched), and is not told. Not replayed: read first.
 */
export function subscribeMasterWorld(fn: () => void): () => void {
  let last = signature(readMasterWorld())
  const check = (): void => {
    const read = readMasterWorld()
    recoverUnsettledWorld(read)
    const now = signature(read)
    if (now.length === last.length && now.every((v, i) => v === last[i])) return
    last = now
    fn()
  }
  const unsubscribers = [useTabStore.subscribe(check), useWorkspaceStore.subscribe(check), useLocalProfilesStore.subscribe(check)]
  return () => {
    for (const unsubscribe of unsubscribers) unsubscribe()
  }
}

// === Stuck ===

export const MASTER_WORLD_STUCK_MS = 5_000

/** When `masterWorldStuck` first saw the current unsettled stretch, on its CALLER's clock. */
let unsettledSince: number | null = null

/**
 * Has the master world been unsettled for more than five seconds? Between two
 * rehydrates of another window's switch it is unsettled for milliseconds; this
 * long means a store that is not coming (see NOTHING HERE REPAIRS). The stretch
 * is timed from the first call that saw it and forgotten by the first call that
 * sees it settled — so it is the caller that decides when to look (the collector
 * does, on every change it hears of), and there is no timer here to leak into a
 * session without a master.
 */
export function masterWorldStuck(now: number): boolean {
  const read = readMasterWorld()
  recoverUnsettledWorld(read)
  if (read.settled) {
    unsettledSince = null
    return false
  }
  if (unsettledSince === null) unsettledSince = now
  return now - unsettledSince > MASTER_WORLD_STUCK_MS
}

// === Recovering ===

/** A recovery was asked for in the current unsettled stretch; forgotten by the first look that finds it settled. */
let recoveryAsked = false

/**
 * `read` is what the caller has just read. Unsettled by a mismatch, or behind the
 * fence → the three stores read storage again: once per stretch, or — `byUser`,
 * the refusal of something the user asked for — every time (see …EXCEPT ONE
 * REHYDRATE in the header).
 */
export function recoverUnsettledWorld(read: MasterWorldRead, byUser = false): void {
  if (read.settled) {
    recoveryAsked = false
    return
  }
  if (read.reason === 'no-parked-master' || (recoveryAsked && !byUser)) return
  recoveryAsked = true
  queueMicrotask(() => {
    if (readMasterWorld().settled) return // it was a switch of this window, half-way through its block
    // The pointer first, as a switch writes them. Each is synchronous over `localStorage`; one that throws must
    // not keep the others from reading.
    for (const store of [useLocalProfilesStore, useTabStore, useWorkspaceStore]) {
      try {
        void Promise.resolve(store.persist.rehydrate()).catch(() => {})
      } catch {
        // as it was; the next unsettled stretch asks again
      }
    }
  })
}

export function __resetMasterWorldForTest(): void {
  unsettledSince = null
  recoveryAsked = false
}
