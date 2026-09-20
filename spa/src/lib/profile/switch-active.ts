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
// the block captures `useLocalProfilesStore`'s five fields first and puts them
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
// master-world.ts, which restores its two stores itself). And a window that is
// itself BEHIND the fence — settled, all three stores agreeing on a world another
// window has already replaced — is refused like an unsettled one: its switch
// would park an outdated screen over the device's current world. It is told to
// catch up (`recoverUnsettledWorld`), and the next attempt works.
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
// NOT HEAR OF A SESSION THAT CLOSED WHILE IT WAS PARKED. Its panes come back on
// screen still bound to that session and are reconciled by the next `sessions`
// payload of their host. For the master that is exactly a section that was
// offline for a while — nothing new to the sync, which already has to cope with
// it. Rebuild works the same for both kinds of world (decision 13): its inputs
// (`PaneRebuildRecord`) travel with the tab.
//   There is no entry point today that re-runs that reconciliation on demand —
// it is an inline block of the WS handler, fed by the daemon's push — so this
// file does not trigger one (and must not invent a network path to do so). See
// `switchActiveProfile`.
//
// IDS. A copy gets a new id for everything the world MINTS — workspace, tab,
// pane, split — and every reference to one follows (`copyWorld` lists them).
// What it BORROWS is copied as it is: host ids, session codes, tmux instances,
// rebuild records — a slave works on the same sessions (decisions 9, 13).
import { useWorkspaceStore } from '../../features/workspace/store'
import { MASTER_PROFILE_ID, normalizeLocalProfileName, useLocalProfilesStore } from '../../stores/useLocalProfilesStore'
import type { ParkedWorld } from '../../stores/useLocalProfilesStore'
import { useProfileStore } from '../../stores/useProfileStore'
import { useRebuildStore, withOperationLock } from '../../stores/useRebuildStore'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceSettingsStore } from '../../stores/useWorkspaceSettingsStore'
import type { PaneLayout, Tab, Workspace } from '../../types/tab'
import { generateId } from '../id'
import { raiseWorldEpochFence, readWorldEpochFence } from '../storage/world-fence'
import { commitTabWorld, readMasterWorld, recoverUnsettledWorld, restampWorld } from './master-world'

export const PROFILE_SWITCH_LOCK_OWNER = 'profile-switch'

type Refused<R extends string> = { ok: false; reason: R }
/** A store write threw; everything written before it was put back. `detail` is the error's message. */
type WriteFailed = { ok: false; reason: 'write-failed'; detail: string }

const messageOf = (err: unknown): string => (err instanceof Error ? err.message : String(err))
const writeFailed = (err: unknown): WriteFailed => ({ ok: false, reason: 'write-failed', detail: messageOf(err) })

// === The parking lot, captured ===

type LocalSnapshot = Pick<ReturnType<typeof useLocalProfilesStore.getState>, 'slaves' | 'slaveOrder' | 'activeProfileId' | 'parkedMaster' | 'worldEpoch'>

function captureLocal(): LocalSnapshot {
  const s = useLocalProfilesStore.getState()
  return { slaves: s.slaves, slaveOrder: s.slaveOrder, activeProfileId: s.activeProfileId, parkedMaster: s.parkedMaster, worldEpoch: s.worldEpoch }
}

/** As master-world.ts's `restore`: memory is back before persist's storage write can throw. */
function restoreLocal(old: LocalSnapshot): void {
  const now = captureLocal()
  if ((Object.keys(old) as (keyof LocalSnapshot)[]).every((k) => now[k] === old[k])) return // refused, or threw before it wrote
  try {
    useLocalProfilesStore.setState(old)
  } catch {
    // best effort
  }
}

/** What the two live stores hold right now, by reference. */
function readScreen(): ParkedWorld {
  const tab = useTabStore.getState()
  const ws = useWorkspaceStore.getState()
  return { workspaces: ws.workspaces, tabs: tab.tabs, activeWorkspaceId: ws.activeWorkspaceId, activeTabId: tab.activeTabId }
}

// === The switch ===

/**
 * May this window move the world? Settled — and not behind the epoch fence (see
 * THE EPOCH FENCE IS RAISED FIRST). Either way the stores are asked to catch up
 * with storage, so that a refusal is not the last word.
 */
function worldIsCurrent(): boolean {
  const read = readMasterWorld()
  if (!read.settled) {
    recoverUnsettledWorld(read)
    return false
  }
  if (useLocalProfilesStore.getState().worldEpoch < readWorldEpochFence()) {
    recoverUnsettledWorld(read, true)
    return false
  }
  return true
}

export type SwitchResult = { ok: true } | Refused<'busy' | 'unsettled' | 'not-found' | 'already-on-screen' | 'bad-world' | 'bad-epoch'> | WriteFailed

/** THE SYNCHRONOUS BLOCK. Not `async`, on purpose: an `await` cannot be written in here. */
function exchange(targetId: string): SwitchResult {
  if (!worldIsCurrent()) return { ok: false, reason: 'unsettled' }
  const old = captureLocal()
  const worldEpoch = old.worldEpoch + 1
  let lowerFence = (): void => {}
  try {
    lowerFence = raiseWorldEpochFence(worldEpoch)
    const swapped = useLocalProfilesStore.getState().swapActive(targetId, readScreen(), worldEpoch)
    if (!swapped.ok) {
      lowerFence()
      return swapped
    }
    commitTabWorld(swapped.world, undefined, { worldId: targetId, worldEpoch, beforeRollback: lowerFence })
    return { ok: true }
  } catch (err) {
    lowerFence()
    restoreLocal(old)
    return writeFailed(err)
  }
}

/**
 * Puts `targetId`'s world on screen and parks the one that was there. A refusal
 * has written nothing; `write-failed` has put everything back. The exchange has
 * happened — or not — by the time this function RETURNS its promise: the promise
 * is there for the lock's signature, nothing in here waits.
 *
 * NOT DONE HERE, AND WHY (see WHAT A PARKED WORLD DOES NOT HEAR): the plan has
 * the switch ask every connected host for its sessions afterwards, so that the
 * reconciliation runs over the world that has just come on screen. There is
 * nothing to call: `useSessionStore.fetchHost` fills the session LIST and
 * reconciles nothing (and `rebuild/revive.ts` says why its result is not
 * evidence), and the reconciliation itself is an inline block of the WS handler
 * in `useMultiHostEventWs`. Until that block is a function, a world that comes on
 * screen is reconciled by its hosts' next `sessions` payload — which is what
 * happens to every pane of an app that was closed for a while, too.
 */
export function switchActiveProfile(targetId: typeof MASTER_PROFILE_ID | string): Promise<SwitchResult> {
  return withOperationLock<SwitchResult>(
    PROFILE_SWITCH_LOCK_OWNER,
    // `async` with no `await`: the body runs to its `return` inside `withOperationLock`'s own synchronous prefix.
    async () => exchange(targetId),
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
function copyWorld(source: ParkedWorld): WorldCopy {
  const clone = structuredClone(source)
  const taken = idsInUse()
  const fresh = (): string => {
    for (;;) {
      const id = generateId()
      if (taken.has(id)) continue
      taken.add(id)
      return id
    }
  }

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

export type CopyResult = { ok: true; id: string } | Refused<'unsettled' | 'bad-name' | 'bad-world'> | WriteFailed

/**
 * `source`, copied, as a new parked slave — with the workspace-scoped settings
 * (`useWorkspaceSettingsStore`, keyed by workspace id) of its workspaces copied
 * to the new ids: the user asked for the same working environment, and a
 * workspace that silently forgot its settings is not that. Those entries never
 * reach the SOT: the settings section projects the MASTER world's workspace ids
 * only (`masterWorkspaceIds`), and these ids are in no master world.
 */
function addCopyAsSlave(name: string, source: ParkedWorld): CopyResult {
  if (normalizeLocalProfileName(name) === null) return { ok: false, reason: 'bad-name' } // before anything is built
  const old = captureLocal()
  const oldScoped = useWorkspaceSettingsStore.getState().workspaces
  try {
    const { world, workspaceIds } = copyWorld(source)
    const added = useLocalProfilesStore.getState().addSlave(name, world)
    if (!added.ok) return added

    const scoped: typeof oldScoped = {}
    for (const [from, to] of workspaceIds) {
      if (Object.hasOwn(oldScoped, from)) scoped[to] = structuredClone(oldScoped[from])
    }
    if (Object.keys(scoped).length > 0) useWorkspaceSettingsStore.setState({ workspaces: { ...oldScoped, ...scoped } })
    return added
  } catch (err) {
    restoreLocal(old)
    if (useWorkspaceSettingsStore.getState().workspaces !== oldScoped) {
      try {
        useWorkspaceSettingsStore.setState({ workspaces: oldScoped })
      } catch {
        // best effort
      }
    }
    return writeFailed(err)
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
  if (!read.settled) return { ok: false, reason: 'unsettled' }
  return addCopyAsSlave(name, read.world)
}

/**
 * What is ON SCREEN, copied into a new parked slave; the screen does not move.
 * The wizard saves the local state with it before a pull overwrites it
 * (decision 12). Whose world the screen holds does not matter — so this works
 * while unsettled, too: it files nothing under an existing label.
 */
export function saveScreenAsSlave(name: string): CopyResult {
  return addCopyAsSlave(name, readScreen())
}

// === The move ===

export type PromoteResult = { ok: true; demotedId: string } | Refused<'master-attached' | 'busy' | 'unsettled' | 'not-found' | 'bad-name' | 'bad-epoch'> | WriteFailed

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
 */
export function promoteToMaster(slaveId: string, demotedName: string): PromoteResult {
  if (useProfileStore.getState().masterHostId !== null) return { ok: false, reason: 'master-attached' }
  const grant = useRebuildStore.getState().acquireOperationLock(PROFILE_SWITCH_LOCK_OWNER)
  if (grant === null) return { ok: false, reason: 'busy' }
  try {
    if (!worldIsCurrent()) return { ok: false, reason: 'unsettled' }
    const old = captureLocal()
    const worldEpoch = old.worldEpoch + 1
    let lowerFence = (): void => {}
    try {
      lowerFence = raiseWorldEpochFence(worldEpoch)
      const promoted = useLocalProfilesStore.getState().promoteSlave(slaveId, demotedName, worldEpoch)
      if (!promoted.ok) {
        lowerFence()
        return promoted
      }
      restampWorld({ worldId: promoted.activeProfileId, worldEpoch, beforeRollback: lowerFence })
      return { ok: true, demotedId: promoted.demotedId }
    } catch (err) {
      lowerFence()
      restoreLocal(old)
      return writeFailed(err)
    }
  } finally {
    useRebuildStore.getState().releaseOperationLock(grant)
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
