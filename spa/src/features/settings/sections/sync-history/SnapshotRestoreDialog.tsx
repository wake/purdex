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
    <div role="dialog" aria-modal="true" className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 px-4">
      <div className="w-full max-w-md rounded-xl border border-border-default bg-bg-surface p-6 shadow-2xl">
        <h2 className="text-lg font-semibold text-text-primary">{title}</h2>
        <p className="mt-2 text-sm leading-6 text-text-muted">{body}</p>
        {props.pendingConflictCount > 0 && !isPreOpFailed && !isCoverageWarning && (
          <p className="mt-3 rounded-md border border-yellow-500/30 bg-yellow-500/10 px-3 py-2 text-sm text-status-warn-text">
            {t(pluralKey('settings.sync.history.restore.confirmPendingConflicts', props.pendingConflictCount), { n: props.pendingConflictCount })}
          </p>
        )}
        <div className="mt-4 flex justify-end gap-2">
          <button
            type="button"
            onClick={props.onCancel}
            disabled={props.restoring}
            className="rounded-md px-3 py-1.5 text-sm text-text-muted transition-colors hover:bg-surface-hover hover:text-text-primary disabled:opacity-50"
          >
            {t('settings.sync.history.restore.cancel')}
          </button>
          <button
            type="button"
            onClick={props.onConfirm}
            disabled={props.restoring}
            className="rounded-md border border-border-active bg-surface-elevated px-3 py-1.5 text-sm text-text-primary transition-colors hover:border-accent-base disabled:opacity-50"
          >
            {proceedLabel}
          </button>
        </div>
      </div>
    </div>
  )
}
