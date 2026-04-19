import { useI18nStore } from '../../../../stores/useI18nStore'
import { pluralKey } from '../../../../lib/plural'

export type SnapshotRestoreDialogMode = 'confirm' | 'preOpFailed' | 'coverageWarning'

export interface SnapshotRestoreDialogProps {
  open: boolean
  mode?: SnapshotRestoreDialogMode
  pendingConflictCount: number
  missingContributors?: string[]
  onCancel: () => void
  onConfirm: () => void
  restoring: boolean
}

export function SnapshotRestoreDialog(props: SnapshotRestoreDialogProps) {
  const t = useI18nStore((s) => s.t)
  if (!props.open) return null

  const mode = props.mode ?? 'confirm'
  const isPreOpFailed = mode === 'preOpFailed'
  const isCoverageWarning = mode === 'coverageWarning'

  const title = t('settings.sync.history.restore.confirmTitle')
  const body = isPreOpFailed
    ? t('settings.sync.history.restore.preOpFailed')
    : isCoverageWarning
      ? t('settings.sync.history.restore.coverageWarning', {
          names: (props.missingContributors ?? []).join(', '),
        })
      : t('settings.sync.history.restore.confirmBody')
  const proceedLabel = isPreOpFailed || isCoverageWarning
    ? t('settings.sync.history.restore.continueAnyway')
    : t('settings.sync.history.restore.proceed')

  return (
    <div role="dialog" aria-modal="true" className="fixed inset-0 flex items-center justify-center bg-black/40 z-50">
      <div className="bg-bg-surface border border-text-subtle/20 rounded-md p-6 max-w-md">
        <h2 className="text-lg font-semibold text-text-primary">{title}</h2>
        <p className="mt-2 text-sm text-text-muted">{body}</p>
        {props.pendingConflictCount > 0 && !isPreOpFailed && !isCoverageWarning && (
          <p className="mt-2 text-sm text-status-warn-text">
            {t(pluralKey('settings.sync.history.restore.confirmPendingConflicts', props.pendingConflictCount), { n: props.pendingConflictCount })}
          </p>
        )}
        <div className="mt-4 flex justify-end gap-2">
          <button type="button" onClick={props.onCancel} className="px-3 py-1 text-sm text-text-muted">
            {t('settings.sync.history.restore.cancel')}
          </button>
          <button
            type="button"
            onClick={props.onConfirm}
            disabled={props.restoring}
            className="px-3 py-1 text-sm bg-accent-base text-white rounded disabled:opacity-50"
          >
            {proceedLabel}
          </button>
        </div>
      </div>
    </div>
  )
}
