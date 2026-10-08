// spa/src/components/executions/ExecutionsView.tsx — one host's section of the
// activity bar's worker list (`WorkerList`; P-C spec §4.3): header with the host's name and
// its Nexen phase, then the host's live conversations (one row each, `liveEntityRows`) from the shared
// per-host list store (one site-wide SSE per host, shared with the Host → Nex
// table). Nexen readiness comes from `useNexHostStore` only; this view never
// reads `HostInfo.nex` itself.
import { useEffect, useMemo, useState } from 'react'
import { Circle, Spinner } from '@phosphor-icons/react'
import { useHostExecutions } from '../../hooks/useHostExecutions'
import { useListRetry } from '../../hooks/useListRetry'
import { useHostLook } from '../../lib/host-look'
import { useI18nStore } from '../../stores/useI18nStore'
import { selectRollupCostShown, useNexHostStore, type NexHostPhase } from '../../stores/useNexHostStore'
import { openWorkerTab } from '../../features/workspace/lib/open-worker-tab'
import { groupBySource } from '../../lib/nex/execution-groups'
import { liveEntityRows } from '../../lib/nex/live-workers'
import { isRefShownNow, useIsRefShown } from '../../lib/shown-hosts'
import { ExecutionsGroup } from './ExecutionsGroup'
import { useRowExit } from './useRowExit'

export const AGE_TICK_MS = 60_000

function PhaseDot({ phase }: { phase: NexHostPhase }) {
  const common = { size: 8, 'data-testid': 'executions-phase-dot', 'data-phase': phase }
  if (phase === 'loading' || phase === 'unknown') return <Spinner {...common} className="text-yellow-400 animate-spin" />
  if (phase === 'ready') return <Circle {...common} weight="fill" className="text-green-400" />
  if (phase === 'disabled') return <Circle {...common} weight="fill" className="text-text-muted" />
  return <Circle {...common} weight="fill" className="text-red-400" />
}

function useNowTicker(): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), AGE_TICK_MS)
    return () => clearInterval(id)
  }, [])
  return now
}

export function ExecutionsView({ hostId }: { hostId?: string; isActive?: boolean }) {
  const id = hostId ?? ''
  const t = useI18nStore((s) => s.t)
  const hostName = useHostLook(id).name
  const entry = useNexHostStore((s) => s.byHost[id])
  const daemonHostId = typeof entry?.capabilities?.host_id === 'string' ? entry.capabilities.host_id : null
  const showCost = useNexHostStore(selectRollupCostShown(id))
  const { items, phase, error, truncated, refetch } = useHostExecutions(id, { enabled: id !== '' })
  const now = useNowTicker()
  const live = useMemo(() => liveEntityRows(items), [items])
  const groups = useMemo(() => groupBySource(live), [live])
  const shown = useIsRefShown(id === '' ? null : id)
  const { requestExit, pendingIds, dialog: exitDialog } = useRowExit(id, live)
  // The retry keeps its button (busy) while it runs and hands focus on when it settles (#1627 C).
  const { busy: retrying, error: retryError, onRetry, bindButton: bindRetry, bindList } = useListRetry(phase, error, refetch)

  if (id === '') return null

  const nexPhase: NexHostPhase = entry?.phase ?? 'loading'
  // A host hidden in this workbench keeps its executions listed; opening one (it creates a tab) is not offered
  // (plan H2d-2, §0.21 user rules 1 / 5) — its rows are plain, non-action rows and the hint says why.
  const open = (executionId: string) => {
    if (!isRefShownNow(id)) return
    openWorkerTab({ kind: 'execution', executionId, host: id })
  }

  let body: React.ReactNode
  if (nexPhase === 'disabled') {
    body = <p data-testid="executions-disabled" className="px-3 py-2 text-xs text-text-muted">{t('newtab.headless.disabled')}</p>
  } else if (nexPhase === 'unavailable') {
    body = (
      <p data-testid="executions-unavailable" className="px-3 py-2 text-xs text-red-400">
        {t('newtab.headless.unavailable', { error: entry?.error ?? '' })}
      </p>
    )
  } else if (live.length === 0 && phase !== 'ready' && phase !== 'error' && !retrying) {
    body = (
      <div data-testid="executions-loading" className="flex flex-col gap-1.5 px-3 py-2 animate-pulse" aria-busy="true">
        <span className="text-xs text-text-muted">{t('executions.loading')}</span>
        <div className="h-4 rounded bg-surface-secondary" />
        <div className="h-4 w-2/3 rounded bg-surface-secondary" />
      </div>
    )
  } else {
    body = (
      <>
        {(phase === 'error' || retrying) && (
          <div data-testid="executions-error" className="flex items-center gap-2 px-3 py-1.5 text-xs text-red-400">
            <span className="flex-1 min-w-0 truncate">{t('executions.error', { message: retryError ?? '' })}</span>
            <button
              ref={bindRetry}
              type="button"
              data-testid="executions-retry"
              onClick={onRetry}
              disabled={retrying}
              aria-busy={retrying}
              className="shrink-0 px-1.5 py-0.5 rounded text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer disabled:opacity-50 disabled:cursor-default"
            >
              {t('executions.retry')}
            </button>
          </div>
        )}
        {retrying && (
          <p data-testid="executions-loading" role="status" className="px-3 py-1 text-xs text-text-muted">{t('executions.loading')}</p>
        )}
        {truncated && (
          <p data-testid="executions-truncated" className="px-3 py-1 text-xs text-text-muted">{t('executions.truncated')}</p>
        )}
        {live.length === 0 && phase !== 'error' && !retrying && (
          <p ref={bindList} tabIndex={-1} data-testid="executions-empty" className="px-3 py-2 text-xs text-text-muted outline-none">{t('executions.empty')}</p>
        )}
        {groups.length > 0 && (
          // The list as one element: what a successful retry focuses (#1627 C).
          <div ref={bindList} tabIndex={-1} data-testid="executions-list" className="flex flex-col outline-none">
            {groups.map((group) => (
              <ExecutionsGroup key={group.source} group={group} hostId={id} daemonHostId={daemonHostId} now={now} showCost={showCost} onOpen={shown ? open : undefined} onExit={shown ? requestExit : undefined} exitPending={pendingIds} />
            ))}
          </div>
        )}
      </>
    )
  }

  return (
    <div data-testid="executions-view" className="flex flex-col">
      <div data-testid="executions-header" className="flex items-center gap-1.5 px-3 py-1 mt-1 min-w-0">
        <PhaseDot phase={nexPhase} />
        <span className="text-sm font-bold text-text-primary truncate">{hostName ?? id}</span>
      </div>
      {!shown && (
        <p data-testid="executions-open-hint" className="px-3 py-1 text-xs text-text-muted">{t('hosts.shown.open_executions_hint')}</p>
      )}
      {body}
      {exitDialog}
    </div>
  )
}
