// spa/src/components/executions/ExecutionsView.tsx — the sidebar "Executions"
// view for the active host (P-C spec §4.3): header with the host's name and
// its Nexen phase, then the host's non-archived executions from the shared
// per-host list store (one site-wide SSE per host, shared with the Host → Nex
// table). Nexen readiness comes from `useNexHostStore` only; this view never
// reads `HostInfo.nex` itself.
import { useEffect, useMemo, useState } from 'react'
import { Circle, Spinner } from '@phosphor-icons/react'
import type { ViewProps } from '../../lib/module-registry'
import { useHostExecutions } from '../../hooks/useHostExecutions'
import { useHostLook } from '../../lib/host-look'
import { useI18nStore } from '../../stores/useI18nStore'
import { useNexHostStore, type NexHostPhase } from '../../stores/useNexHostStore'
import { useTabStore } from '../../stores/useTabStore'
import { groupBySource } from '../../lib/nex/execution-groups'
import { isRefShownNow, useIsRefShown } from '../../lib/shown-hosts'
import { ExecutionsGroup } from './ExecutionsGroup'

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

export function ExecutionsView({ hostId }: ViewProps) {
  const id = hostId ?? ''
  const t = useI18nStore((s) => s.t)
  const hostName = useHostLook(id).name
  const entry = useNexHostStore((s) => s.byHost[id])
  const daemonHostId = typeof entry?.capabilities?.host_id === 'string' ? entry.capabilities.host_id : null
  const { items, phase, error, refetch } = useHostExecutions(id, { enabled: id !== '' })
  const now = useNowTicker()
  const groups = useMemo(() => groupBySource(items), [items])
  const shown = useIsRefShown(id === '' ? null : id)

  if (id === '') return null

  const nexPhase: NexHostPhase = entry?.phase ?? 'loading'
  // A host hidden in this workbench keeps its executions listed; opening one (it creates a tab) is not offered
  // (plan H2d-2, §0.21 user rules 1 / 5) — its rows are plain, non-action rows and the hint says why.
  const open = (executionId: string) => {
    if (!isRefShownNow(id)) return
    useTabStore.getState().openSingletonTab({ kind: 'execution', executionId, host: id })
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
  } else if (items.length === 0 && phase !== 'ready' && phase !== 'error') {
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
        {phase === 'error' && (
          <div data-testid="executions-error" className="flex items-center gap-2 px-3 py-1.5 text-xs text-red-400">
            <span className="flex-1 min-w-0 truncate">{t('executions.error', { message: error ?? '' })}</span>
            <button
              type="button"
              data-testid="executions-retry"
              onClick={refetch}
              className="shrink-0 px-1.5 py-0.5 rounded text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer"
            >
              {t('executions.retry')}
            </button>
          </div>
        )}
        {items.length === 0 && phase !== 'error' && (
          <p data-testid="executions-empty" className="px-3 py-2 text-xs text-text-muted">{t('executions.empty')}</p>
        )}
        {groups.map((group) => (
          <ExecutionsGroup key={group.source} group={group} daemonHostId={daemonHostId} now={now} onOpen={shown ? open : undefined} />
        ))}
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
    </div>
  )
}
