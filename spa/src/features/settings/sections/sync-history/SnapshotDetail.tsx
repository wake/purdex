import { useI18nStore } from '../../../../stores/useI18nStore'
import type { ContributorDiff } from '../../../../lib/sync/snapshot-diff'
import type { StoredSnapshot } from '../../../../lib/sync/snapshot-types'

export interface SnapshotDetailProps {
  snapshot: StoredSnapshot | null
  diff: ContributorDiff[] | null
  onRestore: () => void
  restoring: boolean
}

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / 1024 / 1024).toFixed(2)} MB`
}

function diffTone(status: ContributorDiff['status']): string {
  switch (status) {
    case 'identical':
      return 'bg-surface-elevated text-text-secondary'
    case 'changed':
      return 'bg-accent-muted text-accent-base'
    case 'missing-in-current':
    case 'missing-in-snapshot':
      return 'bg-yellow-500/10 text-yellow-400'
  }
}

export function SnapshotDetail({ snapshot, diff, onRestore, restoring }: SnapshotDetailProps) {
  const t = useI18nStore((s) => s.t)
  if (!snapshot) {
    return (
      <div className="flex min-h-[24rem] items-center justify-center p-6 text-sm text-text-muted">
        <div className="max-w-sm text-center">
          {t('settings.sync.history.detail.selectPrompt')}
        </div>
      </div>
    )
  }
  return (
    <div className="flex h-full flex-col gap-4 p-5">
      <div className="flex flex-col gap-4 border-b border-border-subtle pb-4 md:flex-row md:items-start md:justify-between">
        <div>
          <p className="text-xs uppercase tracking-[0.18em] text-text-muted">
            {t('settings.sync.history.detail.metadata')}
          </p>
          <h3 className="mt-2 text-lg font-medium text-text-primary">{snapshot.device}</h3>
          <p className="mt-1 text-sm text-text-secondary">{new Date(snapshot.timestamp).toLocaleString()}</p>
        </div>
        <button
          type="button"
          onClick={onRestore}
          disabled={restoring}
          className="rounded-md border border-border-active bg-surface-elevated px-4 py-2 text-sm text-text-primary transition-colors hover:border-accent-base disabled:cursor-not-allowed disabled:opacity-50"
        >
          {t('settings.sync.history.detail.restore')}
        </button>
      </div>

      <section className="grid gap-3 sm:grid-cols-3">
        <div className="rounded-lg border border-border-default bg-bg-surface px-4 py-3">
          <div className="text-[11px] uppercase tracking-[0.16em] text-text-muted">
            {t('settings.sync.history.detail.timestamp')}
          </div>
          <div className="mt-2 text-sm text-text-primary">{new Date(snapshot.timestamp).toLocaleString()}</div>
        </div>
        <div className="rounded-lg border border-border-default bg-bg-surface px-4 py-3">
          <div className="text-[11px] uppercase tracking-[0.16em] text-text-muted">
            {t('settings.sync.history.detail.device')}
          </div>
          <div className="mt-2 text-sm text-text-primary">{snapshot.device}</div>
        </div>
        <div className="rounded-lg border border-border-default bg-bg-surface px-4 py-3">
          <div className="text-[11px] uppercase tracking-[0.16em] text-text-muted">
            {t('settings.sync.history.detail.size')}
          </div>
          <div className="mt-2 text-sm text-text-primary">{formatBytes(snapshot.bundleSize)}</div>
        </div>
      </section>

      {diff && diff.length > 0 && (
        <section className="rounded-lg border border-border-default bg-bg-surface p-4">
          <h3 className="text-sm font-semibold text-text-primary">{t('settings.sync.history.detail.diff.title')}</h3>
          <ul className="mt-3 space-y-2 text-sm">
            {diff.map((d) => (
              <li key={d.id} className="flex items-center justify-between gap-3 rounded-md border border-border-subtle px-3 py-2">
                <span className="text-text-primary">{d.id}</span>
                <span className={`rounded-md px-2 py-1 text-[11px] font-medium ${diffTone(d.status)}`}>
                  {t(`settings.sync.history.detail.diff.${d.status === 'missing-in-snapshot' ? 'missingInSnapshot' : d.status === 'missing-in-current' ? 'missingInCurrent' : d.status}`)}
                </span>
              </li>
            ))}
          </ul>
        </section>
      )}
    </div>
  )
}
