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

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / 1024 / 1024).toFixed(2)} MB`
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
        'w-full rounded-lg border px-3 py-3 text-left transition-colors',
        selected
          ? 'border-border-active bg-surface-elevated'
          : 'border-border-subtle hover:border-border-default hover:bg-surface-hover',
      ].join(' ')}
    >
      <div className="flex items-start gap-3">
        <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-md border border-border-default bg-bg-surface text-text-muted">
          <Icon size={16} className="shrink-0" />
        </div>
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <span className="truncate text-sm font-medium text-text-primary">
              {t(`settings.sync.history.trigger.${meta.trigger === 'pre-import' ? 'preImport' : meta.trigger === 'pre-restore' ? 'preRestore' : meta.trigger}`)}
            </span>
            <span className="text-xs text-text-muted">{formatRelative(meta.timestamp)}</span>
          </div>
          <div className="mt-1 flex flex-wrap gap-x-3 gap-y-1 text-xs text-text-secondary">
            <span>{meta.device}</span>
            <span>{formatBytes(meta.bundleSize)}</span>
          </div>
        </div>
        {meta.isSessionPristine && (
          <span data-testid="pristine-badge" className="shrink-0 rounded-md bg-accent-muted px-2 py-1 text-[11px] font-medium text-accent-base">
            {t('settings.sync.history.trigger.sessionPristine')}
          </span>
        )}
      </div>
    </button>
  )
}
