// spa/src/components/hosts/nex/NexExecutionRow.tsx — one row of the Nex
// executions table (spec §4.4.3). Split out of NexExecutionsTable.tsx so the
// table shell (fetching, SSE refresh, include-archived) stays small even as
// this file grows more per-cell formatting.
import { useI18nStore } from '../../../stores/useI18nStore'
import { getNexClientId } from '../../../lib/nex/client-id'
import { STATE_DOT_CLASSES } from '../../../lib/nex/state-dot'
import { firstLine, shortId } from '../../../lib/nex/format'
import type { ExecutionSummary } from '../../../lib/nex/types'

export interface NexExecutionRowProps {
  row: ExecutionSummary
  confirmingTerminate: boolean
  pending: boolean
  /** Absent → no "Open" (the host is hidden in this workbench — plan H2d-2). */
  onOpen?: () => void
  onTerminateClick: () => void
  onTerminateConfirm: () => void
  onArchiveToggle: () => void
}

function cwdBasename(cwd: string): string {
  const parts = cwd.split('/').filter(Boolean)
  return parts.length > 0 ? parts[parts.length - 1] : cwd
}

/**
 * Relative "time ago" for a past epoch-ms timestamp, reusing the Sync i18n
 * keys — same approach as BackupHistoryList.tsx — so no new keys are needed.
 */
function formatRelativeTime(t: ReturnType<typeof useI18nStore.getState>['t'], ms: number): string {
  const diffSec = Math.max(0, Math.floor((Date.now() - ms) / 1000))
  if (diffSec < 60) return t('settings.sync.time.secondsAgo', { n: diffSec })
  const diffMin = Math.floor(diffSec / 60)
  if (diffMin < 60) return t('settings.sync.time.minutesAgo', { n: diffMin })
  const diffHr = Math.floor(diffMin / 60)
  if (diffHr < 24) return t('settings.sync.time.hoursAgo', { n: diffHr })
  const diffDay = Math.floor(diffHr / 24)
  return t('settings.sync.time.daysAgo', { n: diffDay })
}

export default function NexExecutionRow({
  row,
  confirmingTerminate,
  pending,
  onOpen,
  onTerminateClick,
  onTerminateConfirm,
  onArchiveToggle,
}: NexExecutionRowProps) {
  const t = useI18nStore((s) => s.t)
  // List rows may omit `lease` entirely — render the holder only
  // when present, "—" otherwise. `(you)` decorates a lease this tab holds.
  const isMine = row.lease != null && row.lease.principal_id.endsWith(`/${getNexClientId()}`)

  return (
    <tr className="border-t border-border-subtle hover:bg-surface-secondary/30">
      <td className="px-3 py-2 whitespace-nowrap">
        <span
          className={`inline-block w-2 h-2 rounded-full ${STATE_DOT_CLASSES[row.state] ?? 'bg-text-muted'}`}
          title={row.state}
        />
        <span className="ml-1.5 text-xs text-text-muted">{row.state}</span>
      </td>
      <td className="px-3 py-2 font-mono text-xs text-text-primary" title={row.id}>
        {shortId(row.id)}
      </td>
      <td className="px-3 py-2 text-xs text-text-muted whitespace-nowrap">
        {row.provider}
        {row.effective_profile ? ` · ${row.effective_profile}` : ''}
      </td>
      <td className="px-3 py-2 text-xs text-text-muted font-mono truncate max-w-[160px]" title={row.cwd}>
        {cwdBasename(row.cwd)}
      </td>
      <td className="px-3 py-2 text-xs text-text-primary truncate max-w-[240px]">{firstLine(row.brief)}</td>
      <td className="px-3 py-2 text-xs text-text-muted text-right">{row.observers}</td>
      <td className="px-3 py-2 text-xs text-text-muted" data-testid={`nex-lease-${row.id}`}>
        {row.lease ? (
          <span title={row.lease.principal_id}>
            {row.lease.principal_id}
            {isMine ? ` ${t('hosts.nex.executions.you')}` : ''}
          </span>
        ) : (
          '—'
        )}
      </td>
      <td className="px-3 py-2 text-xs text-text-muted whitespace-nowrap">{row.last_turn_reason ?? '—'}</td>
      <td className="px-3 py-2 text-xs text-text-muted whitespace-nowrap">{formatRelativeTime(t, row.updated_at)}</td>
      <td className="px-3 py-2 text-right">
        <div className="flex items-center justify-end gap-1.5">
          {onOpen && (
            <button
              type="button"
              onClick={onOpen}
              className="px-2 py-1 rounded text-xs text-text-secondary hover:text-accent hover:bg-surface-tertiary cursor-pointer"
            >
              {t('hosts.nex.executions.open')}
            </button>
          )}
          <button
            type="button"
            disabled={pending}
            onClick={confirmingTerminate ? onTerminateConfirm : onTerminateClick}
            className={`px-2 py-1 rounded text-xs cursor-pointer disabled:opacity-50 ${
              confirmingTerminate
                ? 'bg-red-500/20 text-red-400'
                : 'text-text-secondary hover:text-red-400 hover:bg-surface-tertiary'
            }`}
          >
            {confirmingTerminate ? t('hosts.nex.executions.terminate_confirm') : t('hosts.nex.executions.terminate')}
          </button>
          <button
            type="button"
            disabled={pending}
            onClick={onArchiveToggle}
            className="px-2 py-1 rounded text-xs text-text-secondary hover:text-text-primary hover:bg-surface-tertiary cursor-pointer disabled:opacity-50"
          >
            {row.archived ? t('hosts.nex.executions.unarchive') : t('hosts.nex.executions.archive')}
          </button>
        </div>
      </td>
    </tr>
  )
}
