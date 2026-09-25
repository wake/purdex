// spa/src/lib/profile/switch-active.ts — which world is on screen: the switch,
// the one copy, the one move (Profile Sync spec §4.1, decisions 9–13; P3 plan,
// P3b Task 5).
//
// `useLocalProfilesStore` is the parking lot and never touches the tab stores;
// `master-world.ts` writes the tab stores and never moves the pointer. This file
// is where the two meet — the only place a world goes on or off the screen.
//
// THE SWITCH IS ONE SYNCHRONOUS BLOCK (`exchange`): read the screen → `swapActive`
// parks it and hands out the target's world → `commitTabWorld` puts that on
// screen under the new tag. No await in it, so inside this window nothing can run
// between two of the three writes; OTHER windows learn of it store by store, in
// any order, and that is what the epoch barrier of master-world.ts is for — the
// same `worldEpoch` goes into all three stores, and until a window has all three
// it reports nothing.
//
// ALL OR NOTHING. `swapActive` refuses without writing. `commitTabWorld` puts its
// own two stores (and the scoped settings) back when it throws — but by then the
// pointer has moved, and nothing in master-world.ts knows of the parking lot; so
// the block captures `useLocalProfilesStore`'s six fields first and puts them
// back itself. That also covers the parking lot's OWN write throwing: persist
// sets the state in memory and only then writes storage.
//
// A SWITCH NEEDS A SETTLED WORLD. Parking the screen files it under
// `activeProfileId`. While the three stores disagree (another window's switch has
// arrived here in part) nobody can say whose tabs the screen holds — and a
// slave's tabs filed under "master" is the one thing that must never happen: the
// collector would push them. Refused (`unsettled`); it settles by itself within
// milliseconds, or never (master-world.ts, NOTHING HERE REPAIRS).
//
// THE EPOCH FENCE IS RAISED FIRST (lib/storage/world-fence.ts). Before any of the
// three stores is written the block puts the new epoch into the side key, so from
// that instant no other window can persist a store of the old world over what
// follows. A refusal or a throw lowers it again — BEFORE the stores are put back,
// because those restores carry the old epoch and would otherwise be dropped by
// this window's own fence (`WorldStamp.beforeRollback` hands the same step to
// master-world.ts, which restores its two stores itself). A window that is itself
// BEHIND the fence — its three stores agreeing on a world another window has
// already replaced — reads unsettled (master-world.ts, `behind-fence`) and is
// refused like any other: its switch would park an outdated screen over the
// device's current world. Every refusal for `unsettled` asks the stores to catch
// up (`recoverUnsettledWorld`), so the next attempt works.
//
// THE EPOCH IS THE OPERATION'S OWN (`nextWorldEpoch`, world-fence.ts — not
// `epoch + 1`), and raising the fence can FAIL: another window's operation got
// there between the draw and the raise. One new draw, then `busy`. Two windows
// can still both be inside their blocks — each passed the door before the other's
// fence was visible. Then the higher epoch wins whole: every store write of the
// other is below the fence and is dropped (and answered with a rehydrate). The
// block therefore LOOKS BACK before it says ok: a fence that is no longer its own
// means its world never reached storage — `superseded`; what it holds in memory
// is about to be replaced by the winner's, and the stores are asked to hurry.
//
// ONE BLOCK AT A TIME, ACROSS WINDOWS (lib/storage/world-lock.ts). A switch and a
// promote run their block under the Web Lock `purdex-world-switch` where the
// browser has one: the fence is raised by read-check-write on `localStorage`,
// which two renderers can interleave, and only a real mutex closes that. The
// block inside is unchanged and still synchronous — the lock decides when it
// starts, nothing else; not granted within 3 s → `busy`. Where there is no Web
// Locks (no secure context) the block runs inside the call as it always did, and
// what is left open is written down in lib/storage/world-fence.ts.
//
// THE OPERATION LOCK. A switch replaces the tab tree, so it takes the lock every
// other tree-rewriter takes (rebuild, snapshot restore, a profile apply): none of
// them is ever half-way through a world that is then swapped from under it, and
// an apply — which answers `busy` to the executor while the lock is held — never
// lands between the read and the write. The lock covers the block and nothing
// after it.
//
// WHAT A PARKED WORLD DOES NOT HEAR. Session reconciliation
// (useMultiHostEventWs.ts, the `sessions` handler; `markTerminatedForGeneration`
// in useTabStore) only ever looks at the tabs on screen, so A PARKED WORLD DOES
// NOT HEAR OF A SESSION THAT CLOSED WHILE IT WAS PARKED. Rebuild works the same
// for both kinds of world (decision 13): its inputs (`PaneRebuildRecord`) travel
// with the tab.
//   So the world that has just come on screen is reconciled against a list read
// AFTER the switch — and nothing here asks for it: the switch runs under the
// operation lock, and every release of that lock reconciles each host with a
// live, versioned connection from a fresh list (`reconcileAfterLockRelease`,
// rebuild/refresh-sessions.ts; #1255, #1309). The release comes after the block,
// so the epoch fence is already raised: the refresh is fenced by the NEW world,
// and one switch costs one fetch per host. (A refused switch that took the lock
// releases it too: one harmless fetch.) Every verdict of the reconciliation
// changes a binding (`session-closed` is irreversible, `tmux-restarted` and
// revive-by-name re-point the pane) and the master pushes it to the SOT, so the
// list has to be EVIDENCE: `GET /api/sessions?fresh=1` is read by the daemon for
// that request and carries a version (`{epoch, seq}`), and it is applied only
// when it is newer than anything already reconciled for that host — by WS frame
// or fetch (rebuild/session-version.ts) — and only while the world is still the
// one it was fetched for (the epoch fence below has not moved, in any window) and
// the lock has not been taken again. An old daemon answers without a version:
// nothing is fetched, and the world is reconciled by its hosts' next `sessions`
// payload, as before. Revive-by-name never acts on a list reconciled for a
// different world (rebuild/revive.ts): the lock release does not revive the new
// world from the old list. Only this window fetches; the others receive the
// reconciled tab tree through the same rehydrate that brought them the new world.
//
// IDS. A copy gets a new id for everything the world MINTS — workspace, tab,
// pane, split — and every reference to one follows (`copyWorld` lists them).
// What it BORROWS is copied as it is: host ids, session codes, tmux instances,
// rebuild records — a slave works on the same sessions (decisions 9, 13).
import { tabOwnershipQuiet } from '../../features/workspace/lib/adopt-standalone'
import { UNSORTED_WORKSPACE_ID, useWorkspaceStore } from '../../features/workspace/store'
import { useI18nStore } from '../../stores/useI18nStore'
import { MASTER_PROFILE_ID, normalizeLocalProfileName, useLocalProfilesStore } from '../../stores/useLocalProfilesStore'
import type { ParkedWorld } from '../../stores/useLocalProfilesStore'
import { masterAttachedInStorage, useProfileStore } from '../../stores/useProfileStore'
import { useRebuildStore } from '../../stores/useRebuildStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceSettingsStore } from '../../stores/useWorkspaceSettingsStore'
import { createWorkspace } from '../../types/tab'
import type { PaneLayout, Tab, Workspace } from '../../types/tab'
import { nextWorkspaceName } from '../../features/workspace/lib/workspace-naming'
import { generateId } from '../id'
import { STORAGE_KEYS } from '../storage/keys'
import { isWorldEpoch, nextWorldEpoch, persistedWorldEpoch, raiseWorldEpochFence, readWorldEpochFence } from '../storage/world-fence'
import { commitTabWorld, readMasterWorld, recoverUnsettledWorld, restampWorld, RestampRollbackIncomplete } from './master-world'
import type { MasterWorldRead } from './master-world'
import { withWorldLock } from '../storage/world-lock'
import { currentShownIdsNow, masterShownIdsNow } from '../shown-hosts'
import { repairTabOwnership } from './sections'

export const PROFILE_SWITCH_LOCK_OWNER = 'profile-switch'

type Refused<R extends string> = { ok: false; reason: R }
/** A store write threw; everything written before it was put back. `detail` is the error's message. */
type WriteFailed = { ok: false; reason: 'write-failed'; detail: string }

const messageOf = (err: unknown): string => (err instanceof Error ? err.message : String(err))
const writeFailed = (err: unknown): WriteFailed => ({ ok: false, reason: 'write-failed', detail: messageOf(err) })

// === The parking lot, captured ===

type LocalSnapshot = Pick<ReturnType<typeof useLocalProfilesStore.getState>, 'slaves' | 'slaveOrder' | 'activeProfileId' | 'parkedMaster' | 'worldEpoch' | 'relabelCount' | 'master'>

function captureLocal(): LocalSnapshot {
  const s = useLocalProfilesStore.getState()
  return { slaves: s.slaves, slaveOrder: s.slaveOrder, activeProfileId: s.activeProfileId, parkedMaster: s.parkedMaster, worldEpoch: s.worldEpoch, relabelCount: s.relabelCount, master: s.master } // `master`: a promote moves the looks too
}

/** As master-world.ts's `restore`: memory is back before persist's storage write can throw. `false` = the restore
 *  itself threw (a switch and a copy ignore it — best effort; a promote reports it: `rollback-incomplete`). */
function restoreLocal(old: LocalSnapshot): boolean {
  const now = captureLocal()
  if ((Object.keys(old) as (keyof LocalSnapshot)[]).every((k) => now[k] === old[k])) return true // refused, or threw before it wrote
  try {
    useLocalProfilesStore.setState(old)
    return true
  } catch {
    return false
  }
}

/** What the two live stores hold right now, by reference. */
function readScreen(): ParkedWorld {
  const tab = useTabStore.getState()
  const ws = useWorkspaceStore.getState()
  return { workspaces: ws.workspaces, tabs: tab.tabs, activeWorkspaceId: ws.activeWorkspaceId, activeTabId: tab.activeTabId }
}

// === Every tab in exactly one workspace, before a world leaves the screen or is copied ===

/**
 * `world` with every tab in exactly one workspace — the SAME rule as the standing invariant
 * (`repairTabOwnership`; features/workspace/lib/adopt-standalone.ts). That one repairs the world ON SCREEN;
 * a parked world is repaired by nobody, and the parked MASTER keeps syncing: an ownerless tab in it is in no
 * `tabs.<id>` section, never reaches the SOT, and nobody says so. The same reference back when there is
 * nothing to repair (the usual case: nothing is copied, and the pointer is left as it is).
 */
function withOwnershipRepaired(world: ParkedWorld, tabOrder: readonly string[]): ParkedWorld {
  const repaired = repairTabOwnership(
    { workspaces: world.workspaces, tabs: world.tabs, tabOrder, activeTabId: world.activeTabId, activeWorkspaceId: world.activeWorkspaceId },
    { unsortedName: useI18nStore.getState().t('workspace.unsorted'), newWorkspaceId: UNSORTED_WORKSPACE_ID },
  )
  return repaired.membershipChanged ? { ...world, workspaces: repaired.workspaces, activeWorkspaceId: repaired.activeWorkspaceId } : world
}

// === The switch ===

/** May this window move the world? Only a settled one; an unsettled one is asked to catch up with storage, so that a refusal is not the last word. */
function worldIsCurrent(): boolean {
  const read = readMasterWorld()
  recoverUnsettledWorld(read, true)
  return read.settled
}

export type SwitchResult = { ok: true } | Refused<'busy' | 'unsettled' | 'superseded' | 'not-found' | 'already-on-screen' | 'bad-world' | 'bad-epoch'> | WriteFailed

/** The operation's epoch, with the fence raised to it — or null: lost the race for the fence twice (see THE EPOCH IS THE OPERATION'S OWN). */
function openEpoch(): { worldEpoch: number; lowerFence: () => void } | null {
  for (let attempt = 0; attempt < 2; attempt++) {
    const worldEpoch = nextWorldEpoch([useLocalProfilesStore.getState().worldEpoch, useTabStore.getState().worldEpoch, useWorkspaceStore.getState().worldEpoch])
    const lowerFence = raiseWorldEpochFence(worldEpoch)
    if (lowerFence !== null) return { worldEpoch, lowerFence }
  }
  return null
}

/** Did this operation's world reach storage? Not if the fence is another operation's by now — and then the stores are told to catch up with it. */
function superseded(worldEpoch: number): boolean {
  if (readWorldEpochFence() === worldEpoch) return false
  recoverUnsettledWorld(readMasterWorld(), true)
  return true
}

/**
 * THE ONE WAY OUT OF A CORRUPT EPOCH (master-world.ts, `junk-epoch`): a live
 * store holds an epoch that is none, everything else agrees — and STORAGE HOLDS
 * THE SAME JUNK, so the rehydrate the refusal asks for will bring it right back.
 * (Junk in memory over a good record in storage is another window's switch or
 * heal half-way here: that one the rehydrate fixes, and it is refused as ever.)
 * The switch then parks the screen under the label all three stores agree on
 * and stamps a real epoch into all of them. A switch only: a promote relabels,
 * and has no business doing that on a world it cannot read.
 */
function junkEpochForGood(read: MasterWorldRead): boolean {
  if (read.settled || read.reason !== 'junk-epoch') return false
  const live = [
    [STORAGE_KEYS.TABS, useTabStore.getState().worldEpoch],
    [STORAGE_KEYS.WORKSPACES, useWorkspaceStore.getState().worldEpoch],
  ] as const
  return live.every(([key, inMemory]) => isWorldEpoch(inMemory) || !isWorldEpoch(persistedWorldEpoch(key)))
}

/** THE SYNCHRONOUS BLOCK. Not `async`, on purpose: an `await` cannot be written in here. */
function exchange(targetId: string): SwitchResult {
  const read = readMasterWorld()
  if (!read.settled && !junkEpochForGood(read)) {
    recoverUnsettledWorld(read, true) // a refusal is not the last word: the stores are asked to catch up with storage
    return { ok: false, reason: 'unsettled' }
  }
  // WHAT IS PARKED IS REPAIRED FIRST — BUT ONLY A MEMBERSHIP THAT HAS BEEN QUIET. Settled + the world lock + the
  // fence rule out a stale WORLD; they do not rule out another window's `addTab` whose `insertTab` — a second
  // store, a second rehydrate — has not arrived here yet. That tab looks ownerless, and parking now would file
  // it under Unsorted AND park this window's not-yet-rehydrated workspaces over the other window's. So: nothing
  // to repair → go; something to repair and the membership signature quiet for ADOPTION_SETTLE_MS (the very
  // wait the invariant applies) → repair what is parked, never the live stores (a refusal writes nothing);
  // otherwise `busy` — retryable, and by the retry the invariant or the rehydrate has settled it.
  const onScreen = readScreen()
  const toPark = withOwnershipRepaired(onScreen, useTabStore.getState().tabOrder)
  if (toPark !== onScreen && !tabOwnershipQuiet()) return { ok: false, reason: 'busy' }

  const old = captureLocal()
  let lowerFence = (): void => {}
  try {
    const epoch = openEpoch()
    if (epoch === null) return { ok: false, reason: 'busy' }
    const { worldEpoch } = epoch
    lowerFence = epoch.lowerFence
    const swapped = useLocalProfilesStore.getState().swapActive(targetId, toPark, worldEpoch)
    if (!swapped.ok) {
      lowerFence()
      return swapped
    }
    commitTabWorld(swapped.world, undefined, { worldId: targetId, worldEpoch, beforeRollback: lowerFence })
    return superseded(worldEpoch) ? { ok: false, reason: 'superseded' } : { ok: true }
  } catch (err) {
    lowerFence()
    restoreLocal(old)
    return writeFailed(err)
  }
}

/** `block`, synchronously, under this window's operation lock; `busy` when somebody else holds it. Released whatever happens. */
function underOperationLock<R>(block: () => R, busy: R): R {
  const grant = useRebuildStore.getState().acquireOperationLock(PROFILE_SWITCH_LOCK_OWNER)
  if (grant === null) return busy
  try {
    return block()
  } finally {
    useRebuildStore.getState().releaseOperationLock(grant)
  }
}

/**
 * Puts `targetId`'s world on screen and parks the one that was there. A refusal
 * has written nothing; `write-failed` has put everything back. Where there is no
 * Web Locks the exchange has happened — or not — by the time this function
 * RETURNS its promise; with Web Locks, once the cross-window lock is granted (or
 * `busy` after 3 s). Either way the exchange itself never waits.
 *
 * AFTERWARDS the world that has just come on screen is reconciled against a
 * fresh, versioned session list of every connected host — started by the
 * operation lock's release, not by this function (see WHAT A PARKED WORLD DOES
 * NOT HEAR). The result does not wait for it.
 */
export function switchActiveProfile(targetId: typeof MASTER_PROFILE_ID | string): Promise<SwitchResult> {
  return withWorldLock<SwitchResult>(
    () => underOperationLock<SwitchResult>(() => exchange(targetId), { ok: false, reason: 'busy' }),
    () => ({ ok: false, reason: 'busy' }),
  )
}

// === Copying a world ===

/** Every workspace / tab / pane / split id of every world on this device — a fresh id avoids them all. */
function idsInUse(): Set<string> {
  const taken = new Set<string>()
  const layout = (l: PaneLayout): void => {
    if (l.type === 'leaf') {
      taken.add(l.pane.id)
      return
    }
    taken.add(l.id)
    l.children.forEach(layout)
  }
  const local = useLocalProfilesStore.getState()
  const worlds = [readScreen(), local.parkedMaster, ...Object.values(local.slaves).map((s) => s.world)]
  for (const world of worlds) {
    if (world === null) continue
    for (const ws of world.workspaces) taken.add(ws.id)
    for (const tab of Object.values(world.tabs)) {
      taken.add(tab.id)
      layout(tab.layout)
    }
  }
  return taken
}

interface WorldCopy {
  world: ParkedWorld
  /** source workspace id → the copy's. */
  workspaceIds: Map<string, string>
}

/**
 * A deep copy of `source` that shares no object with it, under new ids. What
 * carries an id this world minted, and therefore changes — THE WHOLE LIST, from
 * `types/tab.ts` and `ParkedWorld`:
 *
 *   workspace   `Workspace.id` · `ParkedWorld.activeWorkspaceId`
 *               · `PaneContent{kind:'settings'}.scope.workspaceId`
 *   tab         the key in `ParkedWorld.tabs` · `Tab.id` · `Workspace.tabs[]`
 *               · `Workspace.activeTabId` · `ParkedWorld.activeTabId`
 *   pane/split  `Pane.id` · `SplitLayout.id` — nothing inside a world points at
 *               one (what does is keyed state of OTHER stores — editor pane
 *               state, rebuild operations — which is per mounted pane and is
 *               deliberately not copied: the copy's panes start like new ones)
 *
 * Everything else is copied verbatim, the tmux binding and `rebuild` included.
 * A reference to something the world does not hold (a dangling `activeTabId`)
 * is kept as it is — it was dangling before. `Workspace.moduleConfig` is opaque
 * to this file and holds no id today (files: a project path).
 */
/** A new id that is in `taken` nowhere — and is from now on. */
function freshId(taken: Set<string>): string {
  for (;;) {
    const id = generateId()
    if (taken.has(id)) continue
    taken.add(id)
    return id
  }
}

function copyWorld(source: ParkedWorld): WorldCopy {
  const clone = structuredClone(source)
  const taken = idsInUse()
  const fresh = (): string => freshId(taken)

  const workspaceIds = new Map(clone.workspaces.map((ws) => [ws.id, fresh()]))
  const tabIds = new Map(Object.keys(clone.tabs).map((id) => [id, fresh()]))
  const wsId = (id: string): string => workspaceIds.get(id) ?? id
  const tabId = (id: string): string => tabIds.get(id) ?? id

  const relayout = (layout: PaneLayout): void => {
    if (layout.type === 'split') {
      layout.id = fresh()
      layout.children.forEach(relayout)
      return
    }
    layout.pane.id = fresh()
    const content = layout.pane.content
    if (content.kind === 'settings' && typeof content.scope === 'object') content.scope.workspaceId = wsId(content.scope.workspaceId)
  }

  const tabs: Record<string, Tab> = {}
  for (const [id, tab] of Object.entries(clone.tabs)) {
    tab.id = tabId(id)
    relayout(tab.layout)
    tabs[tab.id] = tab
  }
  const workspaces: Workspace[] = clone.workspaces.map((ws) => ({ ...ws, id: wsId(ws.id), tabs: ws.tabs.map(tabId), activeTabId: ws.activeTabId === null ? null : tabId(ws.activeTabId) }))

  return {
    world: { workspaces, tabs, activeWorkspaceId: clone.activeWorkspaceId === null ? null : wsId(clone.activeWorkspaceId), activeTabId: clone.activeTabId === null ? null : tabId(clone.activeTabId) },
    workspaceIds,
  }
}

/** A write threw AND putting everything back did not fully work either: the store may hold half the operation (a
 *  promote's two stores; a create's new slave). Distinct from `write-failed` on purpose — the user is told to reload
 *  and check (a persistent notice), and must not be invited to press again. */
type RollbackIncomplete = { ok: false; reason: 'rollback-incomplete' }

export type CopyResult = { ok: true; id: string } | Refused<'unsettled' | 'bad-name' | 'bad-world'> | WriteFailed | RollbackIncomplete

/**
 * `source`, copied, as a new parked slave — with the workspace-scoped settings
 * (`useWorkspaceSettingsStore`, keyed by workspace id) of its workspaces copied
 * to the new ids: the user asked for the same working environment, and a
 * workspace that silently forgot its settings is not that. Those entries never
 * reach the SOT: the settings section projects the MASTER world's workspace ids
 * only (`masterWorkspaceIds`), and these ids are in no master world.
 */
function addCopyAsSlave(name: string, source: ParkedWorld, tabOrder: readonly string[], shownHostIds: readonly string[]): CopyResult {
  if (normalizeLocalProfileName(name) === null) return { ok: false, reason: 'bad-name' } // before anything is built
  const old = captureLocal()
  const oldScoped = useWorkspaceSettingsStore.getState().workspaces
  try {
    // A copy is a snapshot and its source stays where it is, so there is no wait here (see `exchange`): at
    // worst a tab whose workspace was still on its way is under Unsorted in the COPY.
    const { world, workspaceIds } = copyWorld(withOwnershipRepaired(source, tabOrder))
    // Its shown-hosts list too (per-workbench shown hosts A7): the source's, copied — `addSlave` sanitises it into a
    // new array, so writing one never writes the other.
    const added = useLocalProfilesStore.getState().addSlave(name, world, shownHostIds)
    if (!added.ok) return added

    const scoped: typeof oldScoped = {}
    for (const [from, to] of workspaceIds) {
      if (Object.hasOwn(oldScoped, from)) scoped[to] = structuredClone(oldScoped[from])
    }
    if (Object.keys(scoped).length > 0) useWorkspaceSettingsStore.setState({ workspaces: { ...oldScoped, ...scoped } })
    return added
  } catch (err) {
    // Both restores are attempted; either failing is `rollback-incomplete` — the new slave may still be there.
    let complete = restoreLocal(old)
    if (useWorkspaceSettingsStore.getState().workspaces !== oldScoped) {
      try {
        useWorkspaceSettingsStore.setState({ workspaces: oldScoped })
      } catch {
        complete = false
      }
    }
    return complete ? writeFailed(err) : { ok: false, reason: 'rollback-incomplete' }
  }
}

/**
 * THE ONLY COPY OF A MASTER (decision 10). The source is the master's world
 * WHEREVER IT IS — `readMasterWorld()`, not the live stores: with a slave on
 * screen the live stores are that slave's. Unsettled → refused; the live stores
 * are no stand-in for a master nobody can locate.
 */
export function copyMasterAsSlave(name: string): CopyResult {
  const read = readMasterWorld()
  recoverUnsettledWorld(read, true)
  if (!read.settled) return { ok: false, reason: 'unsettled' }
  // The master's list, wherever the master is — or nobody can say (a promote half-arrived from another window: the
  // store may still hold the OLD master's list), and then no copy either: `unsettled`, as for the world.
  const shownIds = masterShownIdsNow()
  if (shownIds === null) return { ok: false, reason: 'unsettled' }
  return addCopyAsSlave(name, read.world, [], shownIds)
}

/**
 * What is ON SCREEN, copied into a new parked slave; the screen does not move.
 * The wizard saves the local state with it before a pull overwrites it
 * (decision 12). Whose world the screen holds does not matter — so this works
 * while unsettled, too: it files nothing under an existing label.
 */
export function saveScreenAsSlave(name: string): CopyResult {
  // The CURRENT shown list — the screen's workbench's (lib/shown-hosts.ts). While unsettled that list fails closed and
  // is `[]`, so the copy starts with every host hidden: accepted (hiding closes no tab; the user turns hosts back on).
  return addCopyAsSlave(name, readScreen(), useTabStore.getState().tabOrder, currentShownIdsNow())
}

/**
 * "New blank workbench" (per-workbench plan §0.3): a new parked slave whose world is ONE empty workspace — no tab,
 * nothing active in it — named as a workspace the user adds is (`nextWorkspaceName`, App.tsx's
 * `handleAddWorkspace`; the world has no other name to avoid), and whose shown-hosts list is EMPTY: every host
 * hidden, the user turns hosts on in Settings. The screen does not move. Host looks and every other setting are the
 * master's, as for every slave; the list is the slave's own.
 */
export function createBlankSlave(name: string): CopyResult {
  return addEmptyWorldSlave(name, [])
}

/**
 * "Duplicate settings only" (per-workbench plan §0.2): the blank world of `createBlankSlave` — one empty workspace,
 * no tab — with a copy of the CURRENT shown-hosts list (`currentShownIdsNow`: the workbench on screen's). While
 * nobody can say whose list applies that list is `[]`, and so is the copy's — the same answer `saveScreenAsSlave`
 * ("Duplicate all") gives, and for the same reason: a copy files nothing under an existing label, so it is never
 * refused for an unsettled world; hiding closes no tab, and the user turns hosts back on. The screen does not move.
 */
export function createSettingsCopySlave(name: string): CopyResult {
  return addEmptyWorldSlave(name, currentShownIdsNow())
}

/** One empty workspace as a new parked slave, with `shownHostIds` (copied: `addSlave` sanitises into a new array). */
function addEmptyWorldSlave(name: string, shownHostIds: readonly string[]): CopyResult {
  if (normalizeLocalProfileName(name) === null) return { ok: false, reason: 'bad-name' } // before anything is built
  const old = captureLocal()
  try {
    const workspace: Workspace = { ...createWorkspace(nextWorkspaceName([])), id: freshId(idsInUse()) }
    return useLocalProfilesStore.getState().addSlave(name, { workspaces: [workspace], tabs: {}, activeWorkspaceId: workspace.id, activeTabId: null }, shownHostIds)
  } catch (err) {
    return restoreLocal(old) ? writeFailed(err) : { ok: false, reason: 'rollback-incomplete' }
  }
}

// === The move ===

export type PromoteResult =
  | { ok: true; demotedId: string }
  | Refused<'master-attached' | 'busy' | 'unsettled' | 'superseded' | 'not-found' | 'bad-name' | 'bad-epoch'>
  | WriteFailed
  | RollbackIncomplete

/**
 * A MOVE, never a copy (decision 10: there is no "copy as master"): the slave's
 * world takes the master slot, and the world that held it becomes a slave named
 * `demotedName`. No world goes on or off the screen (`promoteSlave`), so the live
 * stores' content is not written — only their tag, by `restampWorld`, with the
 * same new epoch in all three stores and whatever label the screen now has.
 *
 * ONLY WHILE NO MASTER IS ATTACHED. Attached, this would hand the collector a
 * different world under the master's name and the next push would replace the
 * SOT with it; the wizard stops the sync first, and re-attaches with a direction
 * the user chose.
 *   "ATTACHED" IS ASKED OF STORAGE TOO, under the lock. Another window's attach
 * reaches this window's `useProfileStore` a broadcast and a rehydrate later;
 * until then memory says "no master", the promote would go through, and when the
 * attachment arrives the sync takes the promoted slave for the master — a `push`
 * writes it over the SOT. `masterAttachedInStorage` reads what that window
 * persisted (start.ts does the same for the suspension). What is left: read, then
 * write — not atomic; an attach committed between the two is not seen.
 *   THE OTHER HALF OF THAT RACE — a window attaches while THIS one promotes, and
 * has not heard of the promote — is not `attachMaster`'s to see (it reads no
 * world and takes no lock: an attach is a control-plane act, and the driver may
 * run in a third window anyway). It is the driver's, and the door it goes through
 * answers: the promote raised the epoch fence, so a leader whose stores are still
 * the old ones reads `behind-fence` — it reports nothing and applies nothing
 * until it has caught up, and then the world labelled "master" is the promoted
 * one, which is what the user made it.
 */
export function promoteToMaster(slaveId: string, demotedName: string): Promise<PromoteResult> {
  return withWorldLock<PromoteResult>(
    () => underOperationLock<PromoteResult>(() => relabel(slaveId, demotedName), { ok: false, reason: 'busy' }),
    () => ({ ok: false, reason: 'busy' }),
  )
}

/**
 * THE SYNCHRONOUS BLOCK of a promote. Not `async`, on purpose — as `exchange`.
 *
 * THE SHOWN-HOSTS LIST FOLLOWS THE WORKBENCH (per-workbench shown hosts, plan A3). The master's list lives in
 * `useShownHostsStore`, a local workbench's on its record. So a promote writes TWO stores besides the tags, in this
 * order: the parking lot (`promoteSlave` — the demoted workbench gets the store's list, in its one `set`), then the
 * shown store (the promoted workbench's list, stamped with the new `relabelCount`: a window that has one of the two
 * and not the other reads `[]` — lib/shown-hosts.ts), then the tags. Both before-states are captured before the first
 * write; a throw anywhere — a persist storage write after the in-memory set included — puts every touched store back
 * in reverse. A restore that throws as well (restampWorld's own two included) is `rollback-incomplete`, never folded
 * into `write-failed`.
 */
function relabel(slaveId: string, demotedName: string): PromoteResult {
  if (useProfileStore.getState().masterHostId !== null || masterAttachedInStorage()) return { ok: false, reason: 'master-attached' }
  if (!worldIsCurrent()) return { ok: false, reason: 'unsettled' }
  const old = captureLocal()
  const shownBefore = useShownHostsStore.getState()
  const oldShown = { ids: shownBefore.ids, relabelStamp: shownBefore.relabelStamp }
  let lowerFence = (): void => {}
  try {
    const epoch = openEpoch()
    if (epoch === null) return { ok: false, reason: 'busy' }
    const { worldEpoch } = epoch
    lowerFence = epoch.lowerFence
    const promoted = useLocalProfilesStore.getState().promoteSlave(slaveId, demotedName, worldEpoch, oldShown.ids)
    if (!promoted.ok) {
      lowerFence()
      return promoted
    }
    useShownHostsStore.setState({ ids: promoted.promotedShownHostIds, relabelStamp: useLocalProfilesStore.getState().relabelCount })
    restampWorld({ worldId: promoted.activeProfileId, worldEpoch, beforeRollback: lowerFence })
    return superseded(worldEpoch) ? { ok: false, reason: 'superseded' } : { ok: true, demotedId: promoted.demotedId }
  } catch (err) {
    lowerFence()
    // restampWorld has put its own two stores back (or said it could not); then the shown store, then the parking lot.
    let complete = !(err instanceof RestampRollbackIncomplete)
    if (!restoreShown(oldShown)) complete = false
    if (!restoreLocal(old)) complete = false
    return complete ? writeFailed(err) : { ok: false, reason: 'rollback-incomplete' }
  }
}

/** The shown store's two fields back; `true` when there was nothing to put back. `false` = the restore threw. */
function restoreShown(old: { ids: string[]; relabelStamp: number }): boolean {
  const now = useShownHostsStore.getState()
  if (now.ids === old.ids && now.relabelStamp === old.relabelStamp) return true
  try {
    useShownHostsStore.setState(old)
    return true
  } catch {
    return false
  }
}

// === Thin wrappers ===

export function renameSlave(id: string, name: string): { ok: true } | Refused<'not-found' | 'bad-name'> {
  return useLocalProfilesStore.getState().renameSlave(id, name)
}

export function reorderSlaves(order: string[]): { ok: true } | Refused<'bad-order'> {
  return useLocalProfilesStore.getState().reorderSlaves(order)
}

/**
 * Never the one on screen. The slave's workspaces take their scoped settings
 * with them — nothing else would ever clear those entries — EXCEPT for an id
 * that another world on this device uses as well: a demoted master keeps its
 * ids, and a later attach pulls a master with the very same ones; clearing
 * those would delete the master's settings, and the sync would carry the
 * deletion to every device.
 */
export function deleteSlave(id: string): { ok: true } | Refused<'not-found' | 'on-screen'> {
  const removed = useLocalProfilesStore.getState().removeSlave(id)
  if (!removed.ok) return removed
  const local = useLocalProfilesStore.getState()
  const stillUsed = new Set([readScreen(), local.parkedMaster, ...Object.values(local.slaves).map((s) => s.world)].flatMap((w) => (w === null ? [] : w.workspaces.map((ws) => ws.id))))
  for (const ws of removed.world.workspaces) {
    if (!stillUsed.has(ws.id)) useWorkspaceSettingsStore.getState().clearWorkspace(ws.id)
  }
  return { ok: true }
}
