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

export function SnapshotDetail({ snapshot, diff, onRestore, restoring }: SnapshotDetailProps) {
  const t = useI18nStore((s) => s.t)
  if (!snapshot) {
    return (
      <div className="p-6 text-sm text-text-muted">
        {t('settings.sync.history.detail.selectPrompt')}
      </div>
    )
  }
  return (
    <div className="p-6 flex flex-col gap-4">
      <section>
        <h3 className="text-sm font-semibold text-text-primary">{t('settings.sync.history.detail.metadata')}</h3>
        <dl className="mt-2 text-sm text-text-muted space-y-1">
          <div><dt className="inline">{t('settings.sync.history.detail.timestamp')}:</dt> <dd className="inline">{new Date(snapshot.timestamp).toLocaleString()}</dd></div>
          <div><dt className="inline">{t('settings.sync.history.detail.device')}:</dt> <dd className="inline">{snapshot.device}</dd></div>
          <div><dt className="inline">{t('settings.sync.history.detail.size')}:</dt> <dd className="inline">{formatBytes(snapshot.bundleSize)}</dd></div>
        </dl>
      </section>
      {diff && diff.length > 0 && (
        <section>
          <h3 className="text-sm font-semibold text-text-primary">{t('settings.sync.history.detail.diff.title')}</h3>
          <ul className="mt-2 text-sm space-y-1">
            {diff.map((d) => (
              <li key={d.id} className="flex items-center gap-2">
                <span className="text-text-primary">{d.id}</span>
                <span className="text-text-muted text-xs">{t(`settings.sync.history.detail.diff.${d.status === 'missing-in-snapshot' ? 'missingInSnapshot' : d.status === 'missing-in-current' ? 'missingInCurrent' : d.status}`)}</span>
              </li>
            ))}
          </ul>
        </section>
      )}
      <div>
        <button
          type="button"
          onClick={onRestore}
          disabled={restoring}
          className="px-4 py-2 bg-accent-base text-white rounded disabled:opacity-50"
        >
          {t('settings.sync.history.detail.restore')}
        </button>
      </div>
    </div>
  )
}
