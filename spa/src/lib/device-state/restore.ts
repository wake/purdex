// spa/src/lib/device-state/restore.ts — apply another computer's device state
// to this one (spec §4.3). Structure-only: sessions are re-pointed by name or
// marked terminated, never created (D4).
import { withOperationLock } from '../../stores/useRebuildStore'
import { useHostStore } from '../../stores/useHostStore'
import { buildSnapshot } from '../snapshot/capture'
import { remapLayoutSessions, replaceTabSnapshot, syncSessionStore } from '../snapshot/restore'
import { isWellFormedSnapshotV1 } from '../snapshot/storage'
import { RestoreError } from '../snapshot/types'
import type { RestoreReport, WorkspaceSnapshot } from '../snapshot/types'
import type { Tab } from '../../types/tab'
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

export async function restoreDeviceStateReplace(
  snap: unknown,
  deps?: { now?: number; buildSnapshotFn?: typeof buildSnapshot },
): Promise<DeviceStateRestoreReport> {
  const owner = DEVICE_STATE_LOCK_OWNER.replace
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
      const result: DeviceStateRestoreReport = { ...report, hostRemoved, rebuiltButUnattached: [] }

      try {
        // Back up the current world (structure-only, §4.4) before any store mutation.
        await writeDeviceStatePrev(now, deps?.buildSnapshotFn)
        replaceTabSnapshot(rewritten)
      } catch (cause) {
        throw new RestoreError(result, cause)
      }

      syncSessionStore(remap)
      return result
    },
    (holder) => {
      throw new Error(`${owner} refused: another operation is already running (${holder})`)
    },
  )
}
