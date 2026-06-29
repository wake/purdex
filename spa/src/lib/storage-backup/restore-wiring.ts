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
 * result (`blocked` with conflicts, or `done`).
 *
 * Failure semantics (codex 2c-2 R2 H2): `restoreSnapshot` only ever throws
 * BEFORE it commits `replaceTree` (guard returns blocked; pre-restore / blob /
 * revision failures all abort with the tree untouched), so a throw here IS a
 * true "tree untouched" restore failure the caller may surface as such. Once it
 * returns `done` the tree is committed; the subsequent pane reconciliation runs
 * **best-effort** (never throws) — a reconcile glitch is a UI-alignment issue,
 * NOT a restore rollback, so it must not turn into a restore-failed error.
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
    // Best-effort, post-commit: never let a reconcile glitch surface as a restore
    // failure (the tree is already restored). Failures are logged, not thrown.
    const recon = await applyReconciliation(result.changed, result.restoredFiles, {
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
    if (recon.failed.length > 0) {
      // Non-fatal: restore committed; some panes could not be realigned (they
      // self-heal on next interaction). Surface, never silent (no throw).
      console.warn(
        `restore: ${recon.failed.length} pane(s) could not be reconciled`,
        recon.failed.map((f) => `${f.action.kind}:${f.error}`),
      )
    }
  }
  return result
}
