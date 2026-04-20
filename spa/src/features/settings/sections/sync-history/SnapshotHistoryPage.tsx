import { useEffect, useRef, useState } from 'react'
import { HistoryTabs } from './HistoryTabs'
import { HistoryList } from './HistoryList'
import { SnapshotDetail } from './SnapshotDetail'
import { SnapshotRestoreDialog, type SnapshotRestoreDialogMode } from './SnapshotRestoreDialog'
import { useLocalHistory } from './hooks/useLocalHistory'
import { useSnapshotDiff } from './hooks/useSnapshotDiff'
import {
  useSyncStore,
  PreOpFailedError,
  SnapshotCoverageError,
  RestoreFailedError,
  type RestoreOptions,
} from '../../../../lib/sync/use-sync-store'
import { getSnapshotStore } from '../../../../lib/sync/snapshot-store-instance'
import { useI18nStore } from '../../../../stores/useI18nStore'
import type { StoredSnapshot } from '../../../../lib/sync/snapshot-types'

export function SnapshotHistoryPage() {
  const { items, loading, error, refresh } = useLocalHistory()
  const [activeTab, setActiveTab] = useState<'local' | 'remote'>('local')
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [selectedSnap, setSelectedSnap] = useState<StoredSnapshot | null>(null)
  const [dialogMode, setDialogMode] = useState<SnapshotRestoreDialogMode | 'idle'>('idle')
  const [missingContributors, setMissingContributors] = useState<string[]>([])
  const [warningText, setWarningText] = useState<string | null>(null)
  const [restoring, setRestoring] = useState(false)
  // Overrides accumulate across retries: if the user sees coverageWarning,
  // clicks continue (→ allowMissingContributors), then hits preOpFailed and
  // clicks continue again (→ skipPreOp), the second retry must carry BOTH
  // flags or the coverage check will fire again and deadlock the dialog.
  const [restoreOverrides, setRestoreOverrides] = useState<RestoreOptions>({})
  // Token that identifies the current restore attempt. Bumped whenever the
  // user cancels, switches rows, or a restore resolves. Async completions
  // with a stale token are ignored so an in-flight restore for snapshot A
  // cannot leak its prompt / overrides onto snapshot B after the user
  // cancels and picks another row.
  const restoreTokenRef = useRef(0)

  const activeProviderId = useSyncStore((s) => s.activeProviderId)
  const pendingConflicts = useSyncStore((s) => s.pendingConflicts)
  const restoreFromSnapshot = useSyncStore((s) => s.restoreFromSnapshot)
  const t = useI18nStore((s) => s.t)

  const diff = useSnapshotDiff(selectedSnap)

  useEffect(() => {
    // Clear the detail pane immediately on selection change so a slow
    // getLocal() cannot leave a stale snapshot paired with the new row,
    // which could otherwise feed the restore dialog the wrong bundle.
    setSelectedSnap(null)
    // Switching rows also invalidates any in-flight restore against the
    // previous snapshot — its async result must not mutate dialog state
    // for the newly selected row.
    restoreTokenRef.current++
    if (!selectedId) return
    let cancelled = false
    void getSnapshotStore().getLocal(selectedId).then((snap) => {
      if (!cancelled && snap && snap.id === selectedId) setSelectedSnap(snap)
    })
    return () => { cancelled = true }
  }, [selectedId])

  async function runRestore(next: RestoreOptions = {}, initial = true) {
    const target = selectedSnap
    if (!target) return
    const merged: RestoreOptions = initial ? next : { ...restoreOverrides, ...next }
    const token = ++restoreTokenRef.current
    setRestoring(true)
    setRestoreOverrides(merged)
    try {
      await restoreFromSnapshot(target, 'local', merged)
      if (token !== restoreTokenRef.current) return
      setDialogMode('idle')
      setWarningText(null)
      setRestoreOverrides({})
      await refresh()
    } catch (e) {
      if (token !== restoreTokenRef.current) return
      if (e instanceof PreOpFailedError) {
        setDialogMode('preOpFailed')
      } else if (e instanceof SnapshotCoverageError) {
        setMissingContributors(e.missing)
        setDialogMode('coverageWarning')
      } else if (e instanceof RestoreFailedError) {
        setWarningText(t('settings.sync.history.restore.warnings', { names: e.failed.join(', ') }))
        setDialogMode('idle')
        setRestoreOverrides({})
        await refresh()
      } else {
        throw e
      }
    } finally {
      if (token === restoreTokenRef.current) setRestoring(false)
    }
  }

  async function handleConfirm() {
    if (dialogMode === 'preOpFailed') {
      await runRestore({ skipPreOp: true }, false)
    } else if (dialogMode === 'coverageWarning') {
      await runRestore({ allowMissingContributors: true }, false)
    } else {
      await runRestore()
    }
  }

  function handleCancel() {
    // Invalidate the current restore token so a late resolution/rejection
    // from the in-flight restore cannot reopen the dialog against whatever
    // row the user picks next.
    restoreTokenRef.current++
    setDialogMode('idle')
    setRestoreOverrides({})
    setRestoring(false)
  }

  return (
    <div className="flex h-full flex-col">
      <HistoryTabs
        active={activeTab}
        remoteAvailable={activeProviderId === 'daemon'}
        onChange={setActiveTab}
      />
      <div className="flex flex-1 overflow-hidden">
        <div className="w-80 overflow-y-auto border-r border-text-subtle/20">
          {activeTab === 'local' ? (
            <HistoryList
              items={items}
              loading={loading}
              error={error}
              selectedId={selectedId}
              onSelect={setSelectedId}
              onRetry={refresh}
            />
          ) : (
            <div className="p-6 text-sm text-text-muted">Remote tab: PR B</div>
          )}
        </div>
        <div className="flex-1 overflow-y-auto">
          {warningText && (
            <p role="alert" className="m-4 text-sm text-status-warn-text">
              {warningText}
            </p>
          )}
          <SnapshotDetail
            snapshot={selectedSnap}
            diff={diff}
            onRestore={() => setDialogMode('confirm')}
            restoring={restoring}
          />
        </div>
      </div>
      <SnapshotRestoreDialog
        open={dialogMode !== 'idle'}
        mode={dialogMode === 'idle' ? 'confirm' : dialogMode}
        pendingConflictCount={pendingConflicts.length}
        missingContributors={missingContributors}
        onCancel={handleCancel}
        onConfirm={handleConfirm}
        restoring={restoring}
      />
    </div>
  )
}
