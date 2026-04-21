import { CircleNotch } from '@phosphor-icons/react'
import { HistoryRow } from './HistoryRow'
import { useI18nStore } from '../../../../stores/useI18nStore'
import type { SnapshotMetadata } from '../../../../lib/sync/snapshot-types'

export interface HistoryListProps {
  items: SnapshotMetadata[]
  loading: boolean
  error: Error | null
  selectedId: string | null
  onSelect: (id: string) => void
  onRetry?: () => void
}

export function HistoryList(props: HistoryListProps) {
  const t = useI18nStore((s) => s.t)

  if (props.loading) {
    return (
      <div className="flex min-h-[24rem] items-center justify-center p-6" data-testid="loading">
        <CircleNotch className="animate-spin text-text-muted" size={18} />
      </div>
    )
  }

  if (props.error) {
    return (
      <div className="flex min-h-[24rem] items-center justify-center p-6">
        <div className="max-w-xs text-center text-sm text-text-muted">
          <p>{t('settings.sync.history.error.loadList')}</p>
          {props.onRetry && (
            <button
              type="button"
              onClick={props.onRetry}
              className="mt-3 rounded-md border border-border-default px-3 py-1.5 text-xs text-text-primary hover:border-border-active"
            >
              {t('settings.sync.history.retry')}
            </button>
          )}
        </div>
      </div>
    )
  }

  if (props.items.length === 0) {
    return (
      <div className="flex min-h-[24rem] items-center justify-center p-6" data-testid="empty">
        <div className="max-w-xs text-center text-sm text-text-muted">
          {t('settings.sync.history.empty.local')}
        </div>
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-2 p-3">
      {props.items.map((m) => (
        <HistoryRow
          key={m.id}
          meta={m}
          selected={m.id === props.selectedId}
          onSelect={() => props.onSelect(m.id)}
        />
      ))}
    </div>
  )
}
