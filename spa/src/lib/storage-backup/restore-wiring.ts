/**
 * restore-wiring — bind the headless restore orchestrator (`restoreSnapshot`)
 * and pane reconciliation (`applyReconciliation`) to the live stores + In-App
 * backend (Phase 2c T7). The UI calls `runRestore`; everything store/React-
 * coupled lives here so `restore.ts`/`reconcile-panes.ts` stay headless.
 */
import { restoreSnapshot, type RestoreResult } from './restore'
import { applyReconciliation } from './reconcile-panes'
import { findRestoreConflicts } from './restore-guard'
import { getSnapshot, getBlob } from './backup-api'
import { getFsBackend } from '../fs-backend'
import { useBackupStore } from '../../stores/useBackupStore'
import { useTabStore } from '../../stores/useTabStore'
import { useEditorStore } from '../../stores/useEditorStore'

/**
 * Restore the active host's In-App tree to `snapshotId`. Returns the orchestrator
 * result (`blocked` with conflicts, or `done`). On `done` it reconciles every
 * open In-App pane (close/reload/remount) against the restore's `changed` diff.
 * Throws (the orchestrator guarantees the tree is untouched/rolled back) on a
 * pre-restore or blob-verify failure — the caller surfaces it.
 */
export async function runRestore(hostId: string, snapshotId: number): Promise<RestoreResult> {
  const backend = getFsBackend({ type: 'inapp' })
  if (!backend) throw new Error('restore: In-App backend unavailable')

  const result = await restoreSnapshot({
    hostId,
    snapshotId,
    backend,
    findConflicts: () =>
      findRestoreConflicts({
        tabs: useTabStore.getState().tabs,
        buffers: useEditorStore.getState().buffers,
      }),
    preRestore: () =>
      useBackupStore.getState().backupNow(hostId, { trigger: 'pre-restore', forcePost: true }),
    getSnapshot,
    getBlob,
  })

  if (result.status === 'done') {
    await applyReconciliation(result.changed, {
      getTabs: () => useTabStore.getState().tabs,
      readFile: async (fullPath) => {
        const bytes = await backend.read(fullPath)
        const stat = await backend.stat(fullPath)
        return {
          content: new TextDecoder().decode(bytes),
          stat: { mtime: stat.mtime, size: stat.size },
        }
      },
      closeTabPane: (tabId, paneId) => useTabStore.getState().closePane(tabId, paneId),
      closeEditorPane: (paneId, key) => useEditorStore.getState().closePane(paneId, key),
      reloadBuffer: (key, content, stat) => useEditorStore.getState().reloadBuffer(key, content, stat),
      remountPane: (tabId, paneId) => useTabStore.getState().remountPane(tabId, paneId),
    })
  }
  return result
}
