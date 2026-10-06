// spa/src/components/settings/WorkerExitedTab.tsx — Settings → Worker → Exited: one host's conversations with no
// live stint (spec §9 / D10), searchable. Rebuild opens the worker's ended screen in a tab; its choices do the rest.
import { useEffect, useMemo, useState } from 'react'
import { useHostExecutions } from '../../hooks/useHostExecutions'
import { useExecutionHistory } from '../../hooks/useExecutionHistory'
import { useI18nStore } from '../../stores/useI18nStore'
import { useTabStore } from '../../stores/useTabStore'
import { exitedEntities, matchesExitedQuery } from '../../lib/nex/exited-entities'
import { liveTerminalSessionIds } from '../../lib/nex/terminal-session-ids'
import { LIST_MAX_PAGES, LIST_PAGE_LIMIT } from '../../lib/nex/list-all-executions'
import { relativeAge } from '../../lib/nex/relative-age'
import { workerLabel } from '../../lib/nex/worker-label'
import { openWorkerTab } from '../../features/workspace/lib/open-worker-tab'

const AGE_TICK_MS = 60_000
const basename = (p: string): string => p.replace(/[\\/]+$/, '').split(/[\\/]/).pop() || p

export function WorkerExitedTab({ hostId }: { hostId?: string }) {
  if (!hostId) return null
  return <ExitedList hostId={hostId} />
}

function ExitedList({ hostId }: { hostId: string }) {
  const t = useI18nStore((s) => s.t)
  const { items, phase, error, truncated, refetch } = useExecutionHistory(hostId)
  // Holds the host's shared live subscription: its refreshRevision is what tells the history hook to refetch.
  const { items: liveItems } = useHostExecutions(hostId)
  const tabs = useTabStore((s) => s.tabs)
  const [query, setQuery] = useState('')
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), AGE_TICK_MS)
    return () => clearInterval(id)
  }, [])

  const exited = useMemo(() => exitedEntities(items, liveItems), [items, liveItems])
  // `tabs` is the dependency: the set is derived from the store's panes.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const inTerminal = useMemo(() => liveTerminalSessionIds(hostId), [hostId, tabs])
  const rows = useMemo(() => exited.filter((r) => matchesExitedQuery(r, query)), [exited, query])

  return (
    <div className="flex flex-col gap-2">
      <input
        type="search"
        data-testid="worker-exited-search"
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        placeholder={t('settings.worker.exited.search')}
        aria-label={t('settings.worker.exited.search')}
        className="w-full rounded border border-border-default bg-surface-secondary px-2 py-1 text-sm text-text-primary"
      />
      {phase === 'error' && (
        <div data-testid="worker-exited-error" className="flex items-center gap-2 text-xs text-red-400">
          <span className="flex-1 min-w-0 truncate">{t('newtab.workers.error', { error: error ?? '' })}</span>
          <button type="button" data-testid="worker-exited-retry" onClick={refetch}
            className="shrink-0 px-1.5 py-0.5 rounded text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer">
            {t('newtab.workers.retry')}
          </button>
        </div>
      )}
      {truncated && (
        <p data-testid="worker-exited-truncated" className="text-xs text-text-muted">
          {t('settings.worker.exited.truncated', { n: LIST_PAGE_LIMIT * LIST_MAX_PAGES })}
        </p>
      )}
      {phase === 'loading' && items.length === 0 && (
        <p data-testid="worker-exited-loading" className="text-xs text-text-muted" aria-busy="true">{t('executions.loading')}</p>
      )}
      {phase === 'ready' && rows.length === 0 && (
        <p data-testid="worker-exited-empty" className="text-xs text-text-muted">{t('settings.worker.exited.empty')}</p>
      )}
      {rows.length > 0 && (
        <div role="list" className="flex flex-col">
          {rows.map((row) => {
            const age = relativeAge(row.updated_at, now)
            const running = !!row.session_id && inTerminal.has(row.session_id)
            return (
              <div key={row.id} role="listitem" data-testid="worker-exited-row"
                className="flex items-center gap-2 px-2 py-1.5 text-sm border-b border-border-subtle">
                <span className="flex-1 min-w-0 truncate text-text-primary">{workerLabel(row)}</span>
                <span className="shrink-0 max-w-[10rem] truncate text-xs text-text-muted" title={row.cwd}>{basename(row.cwd)}</span>
                <span className="shrink-0 text-xs text-text-muted">{t(`executions.age.${age.key}`, { n: age.n })}</span>
                {running ? (
                  <span data-testid="worker-exited-in-terminal" className="shrink-0 text-xs text-text-secondary">
                    {t('settings.worker.exited.in_terminal')}
                  </span>
                ) : (
                  <button type="button" data-testid="worker-exited-rebuild"
                    onClick={() => { openWorkerTab({ kind: 'execution', executionId: row.id, host: hostId }) }}
                    className="shrink-0 px-1.5 py-0.5 rounded text-xs text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer">
                    {t('settings.worker.exited.rebuild')}
                  </button>
                )}
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}
