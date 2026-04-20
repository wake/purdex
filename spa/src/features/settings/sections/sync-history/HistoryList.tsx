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
      <div className="flex items-center justify-center p-6" data-testid="loading">
        <CircleNotch className="animate-spin text-text-muted" size={18} />
      </div>
    )
  }

  if (props.error) {
    return (
      <div className="p-6 text-sm text-text-muted">
        {t('settings.sync.history.error.loadList')}
        {props.onRetry && (
          <button type="button" onClick={props.onRetry} className="ml-2 text-accent-base">
            {t('settings.sync.history.retry')}
          </button>
        )}
      </div>
    )
  }

  if (props.items.length === 0) {
    return (
      <div className="p-6 text-sm text-text-muted" data-testid="empty">
        {t('settings.sync.history.empty.local')}
      </div>
    )
  }

  return (
    <div className="flex flex-col">
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
