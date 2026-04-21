import { useEffect, useRef, useState } from 'react'
import { ClockCounterClockwise, WarningCircle } from '@phosphor-icons/react'
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
  const remoteAvailable = activeProviderId === 'daemon'

  const diff = useSnapshotDiff(selectedSnap)

  useEffect(() => {
    // Clear the detail pane immediately on selection change so a slow
    // getLocal() cannot leave a stale snapshot paired with the new row,
    // which could otherwise feed the restore dialog the wrong bundle.
    setSelectedSnap(null)
    // Only invalidate the restore token when no restore is in flight.
    // If we bumped the token while a restore was running, the success
    // path's final setRestoring(false) would be skipped by the token
    // guard and the UI would stay locked in the restoring indicator.
    // The dialog already blocks Cancel while restoring, so selection
    // changes mid-restore cannot actually dispatch a second restore.
    if (!restoring) restoreTokenRef.current++
    if (!selectedId) return
    let cancelled = false
    void getSnapshotStore().getLocal(selectedId).then((snap) => {
      if (!cancelled && snap && snap.id === selectedId) setSelectedSnap(snap)
    })
    return () => { cancelled = true }
  }, [selectedId, restoring])

  async function runRestore(next: RestoreOptions = {}, initial = true) {
    const target = selectedSnap
    if (!target) return
    const merged: RestoreOptions = initial ? next : { ...restoreOverrides, ...next }
    const token = ++restoreTokenRef.current
    setRestoring(true)
    setRestoreOverrides(merged)
    try {
      await restoreFromSnapshot(target, 'local', merged)
      if (token === restoreTokenRef.current) {
        setDialogMode('idle')
        setWarningText(null)
        setRestoreOverrides({})
        await refresh()
      }
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
      // Always clear the restoring indicator — decoupled from the token so
      // that a later selection change cannot leave the UI permanently stuck
      // in the restoring state. The token still guards post-completion
      // dialog/state mutation above.
      setRestoring(false)
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
    // Cancel is only meaningful when no restore is in flight. Guard with
    // the dialog's disabled prop on restoring, but keep a runtime check
    // here too so programmatic calls can't bypass it. Invalidate the
    // token so any late rejection against the just-closed dialog cannot
    // reopen it against whatever row the user picks next.
    if (restoring) return
    restoreTokenRef.current++
    setDialogMode('idle')
    setRestoreOverrides({})
  }

  return (
    <div className="mx-auto flex h-full w-full max-w-6xl flex-col gap-6">
      <div>
        <div className="flex items-center gap-3">
          <div className="flex h-10 w-10 items-center justify-center rounded-lg border border-border-default bg-surface-secondary text-text-secondary">
            <ClockCounterClockwise size={18} />
          </div>
          <div>
            <h2 className="text-lg text-text-primary">{t('settings.sync.history.title')}</h2>
            <p className="text-xs text-text-secondary">{t('settings.sync.history.description')}</p>
          </div>
        </div>
      </div>

      {warningText && (
        <div
          role="alert"
          className="flex items-start gap-3 rounded-lg border border-yellow-500/30 bg-yellow-500/10 px-4 py-3 text-sm text-status-warn-text"
        >
          <WarningCircle size={18} className="mt-0.5 shrink-0 text-yellow-500" />
          <span>{warningText}</span>
        </div>
      )}

      <div className="flex items-center gap-4">
        <HistoryTabs
          active={activeTab}
          remoteAvailable={remoteAvailable}
          onChange={setActiveTab}
        />
      </div>

      <div className="grid min-h-0 flex-1 gap-4 xl:grid-cols-[minmax(19rem,24rem)_minmax(0,1fr)]">
        <section className="min-h-[24rem] overflow-hidden rounded-lg border border-border-default bg-surface-secondary">
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
            <div className="flex min-h-[24rem] items-center justify-center p-6">
              <div className="max-w-sm text-center">
                <p className="text-sm text-text-primary">{t('settings.sync.history.tabs.remote')}</p>
                <p className="mt-2 text-xs text-text-secondary">
                  {remoteAvailable
                    ? t('settings.sync.history.empty.remote')
                    : t('settings.sync.history.tabs.remoteDaemonOnly')}
                </p>
              </div>
            </div>
          )}
        </section>

        <section className="min-h-[24rem] overflow-hidden rounded-lg border border-border-default bg-surface-secondary">
          <SnapshotDetail
            snapshot={selectedSnap}
            diff={diff}
            onRestore={() => setDialogMode('confirm')}
            restoring={restoring}
          />
        </section>
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
