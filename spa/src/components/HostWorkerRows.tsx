// spa/src/components/HostWorkerRows.tsx — one host's live workers, one row per conversation
// (`liveEntityRows`, spec §4.2 / §9). Shared by the New Tab host block's Workers view and the Settings
// Workers list. Rows come from the shared per-host list store; each row offers 退出 (`useRowExit`), same as the
// activity bar list. Loading, error-with-retry, truncation and empty states are shown here.
import { useEffect, useMemo, useState } from 'react'
import { useHostExecutions } from '../hooks/useHostExecutions'
import { useListRetry } from '../hooks/useListRetry'
import { useI18nStore } from '../stores/useI18nStore'
import { selectRollupCostShown, selectSessionTitleSupported, useNexHostStore } from '../stores/useNexHostStore'
import { filterLiveRows } from '../lib/nex/live-workers'
import { useIsRefShown } from '../lib/shown-hosts'
import { ExecutionRowCompact } from './executions/ExecutionRowCompact'
import { useRowExit } from './executions/useRowExit'

const AGE_TICK_MS = 60_000

export interface HostWorkerRowsProps {
  hostId: string
  onOpen: (executionId: string) => void
  /** Prefix of this component's own testids: `-loading`, `-error`, `-retry`, `-truncated`, `-empty`. */
  testIdPrefix: string
  /** Split by cwd: `normal` drops test cwds (under /private/tmp), `test` keeps only those. Omitted = every live row. */
  filter?: 'normal' | 'test'
  /** Search text over cwd / brief / id / provider (`matchesExecutionQuery`); blank keeps every row. */
  query?: string
  /** Home directory for the `~` form of a cwd in the search. */
  home?: string
  /** Show nothing (not the empty copy) when no row is left; loading, error and truncation still show. */
  hideEmpty?: boolean
}

export function HostWorkerRows({ hostId, onOpen, testIdPrefix, filter, query, home, hideEmpty }: HostWorkerRowsProps) {
  const t = useI18nStore((s) => s.t)
  const entry = useNexHostStore((s) => s.byHost[hostId])
  const daemonHostId = typeof entry?.capabilities?.host_id === 'string' ? entry.capabilities.host_id : null
  const showCost = useNexHostStore(selectRollupCostShown(hostId))
  const { items, phase, error, truncated, refetch } = useHostExecutions(hostId)
  const titleSupported = useNexHostStore(selectSessionTitleSupported(hostId))
  const live = useMemo(() => filterLiveRows(items, { filter, query, home, titleSupported }), [items, filter, query, home, titleSupported])
  const shown = useIsRefShown(hostId)
  const { requestExit, pendingIds, dialog } = useRowExit(hostId, live)
  // The retry keeps its button (busy) while it runs and hands focus on when it settles (#1627 C).
  const { busy: retrying, error: retryError, onRetry, bindButton: bindRetry, bindList } = useListRetry(phase, error, refetch)
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), AGE_TICK_MS)
    return () => clearInterval(id)
  }, [])

  // Only a ready Nexen host has a list to load; the others get their state (same copy as `ExecutionsView`).
  const nexPhase = entry?.phase ?? 'loading'
  if (nexPhase === 'disabled') {
    return <p data-testid={`${testIdPrefix}-disabled`} className="px-3 py-2 text-xs text-text-muted">{t('newtab.headless.disabled')}</p>
  }
  if (nexPhase === 'unavailable') {
    return (
      <p data-testid={`${testIdPrefix}-unavailable`} className="px-3 py-2 text-xs text-red-400">
        {t('newtab.headless.unavailable', { error: entry?.error ?? '' })}
      </p>
    )
  }

  if (live.length === 0 && phase !== 'ready' && phase !== 'error' && !retrying) {
    return (
      <div data-testid={`${testIdPrefix}-loading`} className="flex flex-col gap-1.5 px-3 py-2 animate-pulse" aria-busy="true">
        <span className="text-xs text-text-muted">{t('executions.loading')}</span>
        <div className="h-4 rounded bg-surface-secondary" />
        <div className="h-4 w-2/3 rounded bg-surface-secondary" />
      </div>
    )
  }

  return (
    <div className="flex flex-col">
      {(phase === 'error' || retrying) && (
        <div data-testid={`${testIdPrefix}-error`} className="flex items-center gap-2 px-3 py-1.5 text-xs text-red-400">
          <span className="flex-1 min-w-0 truncate">{t('newtab.workers.error', { error: retryError ?? '' })}</span>
          <button
            ref={bindRetry}
            type="button"
            data-testid={`${testIdPrefix}-retry`}
            onClick={onRetry}
            disabled={retrying}
            aria-busy={retrying}
            className="shrink-0 px-1.5 py-0.5 rounded text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer disabled:opacity-50 disabled:cursor-default"
          >
            {t('newtab.workers.retry')}
          </button>
        </div>
      )}
      {retrying && (
        <p data-testid={`${testIdPrefix}-loading`} role="status" className="px-3 py-1 text-xs text-text-muted">{t('executions.loading')}</p>
      )}
      {truncated && (
        <p data-testid={`${testIdPrefix}-truncated`} className="px-3 py-1 text-xs text-text-muted">{t('executions.truncated')}</p>
      )}
      {live.length === 0 && phase !== 'error' && !retrying && !hideEmpty && (
        <p ref={bindList} tabIndex={-1} data-testid={`${testIdPrefix}-empty`} className="px-3 py-2 text-xs text-text-muted outline-none">{t('newtab.workers.empty')}</p>
      )}
      {live.length > 0 && (
        <div ref={bindList} tabIndex={-1} role="list" className="flex flex-col outline-none">
          {live.map((row) => (
            <ExecutionRowCompact
              key={row.id}
              row={row}
              hostId={hostId}
              daemonHostId={daemonHostId}
              now={now}
              showCost={showCost}
              onOpen={shown ? () => onOpen(row.id) : undefined}
              onExit={shown ? () => requestExit(row) : undefined}
              exitPending={pendingIds.has(row.id)}
            />
          ))}
        </div>
      )}
      {dialog}
    </div>
  )
}
