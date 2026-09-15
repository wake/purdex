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
import { isWellFormedSnapshotV1 } from '../snapshot/storage'
import { RestoreError } from '../snapshot/types'
import type { RestoreReport, WorkspaceSnapshot } from '../snapshot/types'
import type { Tab } from '../../types/tab'
import { mergeDeviceState } from './merge'
import type { MergeReport, TabWorld } from './merge'
import { writeDeviceStatePrev } from './prev'
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
        // Back up the current world (structure-only, §4.4) before any store mutation.
        await writeDeviceStatePrev(now, deps?.buildSnapshotFn)
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
