import { ClockClockwise, FloppyDisk, ShieldCheck, Upload } from '@phosphor-icons/react'
import { useI18nStore } from '../../../../stores/useI18nStore'
import type { SnapshotMetadata } from '../../../../lib/sync/snapshot-types'

const TRIGGER_ICON: Record<SnapshotMetadata['trigger'], typeof FloppyDisk> = {
  auto: ClockClockwise,
  manual: FloppyDisk,
  'pre-import': Upload,
  'pre-restore': ShieldCheck,
}

function formatRelative(ms: number): string {
  const diff = Date.now() - ms
  const minutes = Math.floor(diff / 60000)
  if (minutes < 1) return 'just now'
  if (minutes < 60) return `${minutes}m`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h`
  const days = Math.floor(hours / 24)
  return `${days}d`
}

export interface HistoryRowProps {
  meta: SnapshotMetadata
  selected: boolean
  onSelect: () => void
}

export function HistoryRow({ meta, selected, onSelect }: HistoryRowProps) {
  const t = useI18nStore((s) => s.t)
  const Icon = TRIGGER_ICON[meta.trigger]
  return (
    <button
      type="button"
      data-selected={selected}
      onClick={onSelect}
      className={[
        'w-full px-3 py-2 flex items-center gap-2 text-left',
        'border-b border-text-subtle/10',
        selected ? 'bg-accent-muted' : 'hover:bg-text-subtle/5',
      ].join(' ')}
    >
      <Icon size={16} className="text-text-muted" />
      <div className="flex-1">
        <div className="text-sm text-text-primary">
          {t(`settings.sync.history.trigger.${meta.trigger === 'pre-import' ? 'preImport' : meta.trigger === 'pre-restore' ? 'preRestore' : meta.trigger}`)}
          <span className="ml-2 text-xs text-text-muted">{formatRelative(meta.timestamp)}</span>
        </div>
        <div className="text-xs text-text-muted">{meta.device}</div>
      </div>
      {meta.isSessionPristine && (
        <span data-testid="pristine-badge" className="text-xs px-1 rounded bg-accent-muted text-accent-base">
          {t('settings.sync.history.trigger.sessionPristine')}
        </span>
      )}
    </button>
  )
}
