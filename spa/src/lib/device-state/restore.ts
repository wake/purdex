// spa/src/lib/device-state/restore.ts — apply another computer's device state
// to this one: replace (spec §4.3) or additive merge (spec §5.4). Structure-only:
// sessions are re-pointed by name or marked terminated, never created (D4).
import { withOperationLock } from '../../stores/useRebuildStore'
import { useHostStore } from '../../stores/useHostStore'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { generateId } from '../id'
import { buildSnapshot } from '../snapshot/capture'
import { remapLayoutSessions, replaceTabSnapshot, syncSessionStore } from '../snapshot/restore'
import { isWellFormedSnapshotV1, writePrevSnapshot } from '../snapshot/storage'
import { RestoreError } from '../snapshot/types'
import type { RestoreReport, WorkspaceSnapshot } from '../snapshot/types'
import type { Tab } from '../../types/tab'
import { mergeDeviceState } from './merge'
import type { MergeReport, TabWorld } from './merge'
import { buildDeviceStatePrev } from './prev'
import { markMissingHosts, reattachByName } from './reattach'

export const DEVICE_STATE_LOCK_OWNER = {
  replace: 'snapshot:deviceStateReplace',
  merge: 'snapshot:deviceStateMerge',
} as const

/**
 * `RestoreError.report` stays typed `RestoreReport`; callers read the extra
 * field via `(e.report as Partial<DeviceStateRestoreReport>).hostRemoved ?? 0`.
 */
export interface DeviceStateRestoreReport extends RestoreReport {
  hostRemoved: number
}

/** On `RestoreError`, the merge counts are all zero (nothing was merged). */
export interface DeviceStateMergeReport extends DeviceStateRestoreReport, MergeReport {}

export const PREV_CAPTURE_ATTEMPTS = 3

interface WorldRefs {
  tabs: unknown
  tabOrder: unknown
  activeTabId: unknown
  workspaces: unknown
  activeWorkspaceId: unknown
}

function worldRefs(): WorldRefs {
  const { tabs, tabOrder, activeTabId } = useTabStore.getState()
  const { workspaces, activeWorkspaceId } = useWorkspaceStore.getState()
  return { tabs, tabOrder, activeTabId, workspaces, activeWorkspaceId }
}

function sameWorld(a: WorldRefs, b: WorldRefs): boolean {
  return (
    a.tabs === b.tabs &&
    a.tabOrder === b.tabOrder &&
    a.activeTabId === b.activeTabId &&
    a.workspaces === b.workspaces &&
    a.activeWorkspaceId === b.activeWorkspaceId
  )
}

/**
 * Build the `-prev` backup in memory and make sure it still describes the live
 * world once the build (which awaits the network) settles. `buildSnapshot`
 * reads the stores synchronously at its start, so the refs taken immediately
 * before the call are the ones the backup was built from. If the user changed
 * tabs/workspaces during the await, rebuild — otherwise Undo would drop state
 * that existed before the mutation. Gives up after PREV_CAPTURE_ATTEMPTS builds.
 *
 * Nothing is persisted here: the caller writes the returned backup right before
 * mutating the stores, so a refused restore leaves the existing `-prev` intact
 * (spec §6).
 */
async function captureStablePrev(
  now: number,
  build: RestoreDeps['buildSnapshotFn'],
): Promise<WorkspaceSnapshot> {
  for (let attempt = 0; attempt < PREV_CAPTURE_ATTEMPTS; attempt++) {
    const capturedFrom = worldRefs()
    const candidate = await buildDeviceStatePrev(now, build)
    if (sameWorld(capturedFrom, worldRefs())) return candidate
  }
  throw new Error('workspace changed during restore; try again')
}

interface RestoreDeps {
  now?: number
  buildSnapshotFn?: typeof buildSnapshot
}

/**
 * Shared pipeline: lock → shape guard → markMissingHosts → reattachByName →
 * per-tab remap → { -prev backup → buildNext → replaceTabSnapshot } → sync.
 *
 * `buildNext` runs synchronously right before `replaceTabSnapshot`, after every
 * await, so a merge reads the stores as they are at mutation time (tabs opened
 * during the network round-trip are kept). A throw anywhere in that block —
 * backup, build, or store replace (which rolls itself back) — becomes a
 * `RestoreError` carrying `errorExtra`.
 */
async function runDeviceStateRestore<Extra extends object>(
  owner: string,
  snap: unknown,
  deps: RestoreDeps | undefined,
  buildNext: (rewritten: WorkspaceSnapshot, now: number) => { next: WorkspaceSnapshot; extra: Extra },
  errorExtra: Extra,
): Promise<DeviceStateRestoreReport & Extra> {
  return withOperationLock(
    owner,
    async () => {
      if (!isWellFormedSnapshotV1(snap)) throw new Error('malformed device state payload')
      const now = deps?.now ?? Date.now()

      const marked = markMissingHosts(snap, new Set(Object.keys(useHostStore.getState().hosts)))
      const { hostRemoved } = marked
      const { remap, report } = await reattachByName(marked.snap.sessionMeta)

      const tabs: Record<string, Tab> = {}
      for (const [id, tab] of Object.entries(marked.snap.tabs)) {
        tabs[id] = { ...tab, layout: remapLayoutSessions(tab.layout, remap, {}) }
      }
      const rewritten: WorkspaceSnapshot = { ...marked.snap, tabs }
      const base: DeviceStateRestoreReport = { ...report, hostRemoved, rebuiltButUnattached: [] }

      let extra: Extra
      try {
        // Capture a stable backup of the current world (structure-only, §4.4).
        const prev = await captureStablePrev(now, deps?.buildSnapshotFn)
        // No await from here to replaceTabSnapshot: the world `-prev` was built
        // from is exactly the world buildNext reads and the mutation replaces.
        writePrevSnapshot(prev)
        const built = buildNext(rewritten, now)
        extra = built.extra
        replaceTabSnapshot(built.next)
      } catch (cause) {
        throw new RestoreError({ ...base, ...errorExtra }, cause)
      }

      syncSessionStore(remap)
      return { ...base, ...extra }
    },
    (holder) => {
      throw new Error(`${owner} refused: another operation is already running (${holder})`)
    },
  )
}

export async function restoreDeviceStateReplace(
  snap: unknown,
  deps?: RestoreDeps,
): Promise<DeviceStateRestoreReport> {
  return runDeviceStateRestore(
    DEVICE_STATE_LOCK_OWNER.replace,
    snap,
    deps,
    (rewritten) => ({ next: rewritten, extra: {} }),
    {},
  )
}

export async function restoreDeviceStateMerge(
  snap: unknown,
  deps?: RestoreDeps & { idGen?: () => string },
): Promise<DeviceStateMergeReport> {
  return runDeviceStateRestore<MergeReport>(
    DEVICE_STATE_LOCK_OWNER.merge,
    snap,
    deps,
    (rewritten, now) => {
      const tabState = useTabStore.getState()
      const wsState = useWorkspaceStore.getState()
      const current: TabWorld = {
        tabs: tabState.tabs,
        tabOrder: tabState.tabOrder,
        activeTabId: tabState.activeTabId,
        workspaces: wsState.workspaces,
        activeWorkspaceId: wsState.activeWorkspaceId,
      }
      const { next, report } = mergeDeviceState(current, rewritten, deps?.idGen ?? generateId)
      return { next: { version: 1, capturedAt: now, sessionMeta: {}, ...next }, extra: report }
    },
    { addedWorkspaces: 0, addedTabs: 0, skippedTabs: 0 },
  )
}
