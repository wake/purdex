// spa/src/components/execution/HeaderStatusChips.tsx — the stream state, the
// observer count and the lease holder, as compact chips next to the header's
// state label (room only; they used to be the worker dock's row). Wide: three
// chips. Narrow (`@max-md`): the chips hide and the same three facts are rows
// of the overflow panel (`HeaderStatusRows`).
import { useI18nStore } from '../../stores/useI18nStore'
import type { ExecutionState } from '../../lib/nex/event-reducer'
import type { ExecutionLeaseView } from '../../lib/nex/types'

export interface HeaderStatus {
  sse: ExecutionState['sse']
  observers: number
  lease?: ExecutionLeaseView | null
  isMine: (principal: string | undefined) => boolean
}

const SSE_DOT: Record<ExecutionState['sse'], string> = {
  open: 'bg-status-success',
  idle: 'bg-status-warning', connecting: 'bg-status-warning', reconnecting: 'bg-status-warning',
  closed: 'bg-status-error',
  paused: 'bg-text-muted',
}

function useFacts({ sse, observers, lease, isMine }: HeaderStatus) {
  const t = useI18nStore((s) => s.t)
  const mine = !!lease && isMine(lease.principal_id)
  return {
    sseText: t(`execution.sse.${sse === 'idle' ? 'connecting' : sse}`),
    observersText: `${observers} ${t('room.dock.observers')}`,
    leaseShort: !lease ? t('execution.lease_none') : `${t('room.dock.lease')}: ${mine ? t('room.dock.you') : lease.principal_id}`,
    leaseFull: !lease ? t('execution.lease_none') : `${lease.principal_id}${mine ? ` ${t('execution.lease_you')}` : ''}`,
    label: { sse: t('room.dock.sse'), observers: t('room.dock.observers'), lease: t('room.dock.lease') },
  }
}

const CHIP = 'shrink-0 inline-flex items-center gap-1 rounded-full bg-surface-hover/60 px-1.5 py-px text-[11px] leading-4 text-text-muted'

/** Wide layout: hidden at `@max-md`, where the overflow panel carries the same facts. */
export function HeaderStatusChips(props: HeaderStatus) {
  const f = useFacts(props)
  return (
    <span data-testid="header-status-chips" className="flex items-center gap-1 min-w-0 @max-md:hidden">
      <span data-testid="header-sse" className={CHIP}>
        <span data-testid="header-sse-dot" className={`w-1.5 h-1.5 rounded-full ${SSE_DOT[props.sse]}`} />
        {f.sseText}
      </span>
      <span data-testid="header-observers" className={CHIP}>{f.observersText}</span>
      <span data-testid="header-lease" title={f.leaseFull} className={`${CHIP} min-w-0 truncate`}>{f.leaseShort}</span>
    </span>
  )
}

/** Narrow layout: the same facts as a small table inside the overflow panel. */
export function HeaderStatusRows(props: HeaderStatus) {
  const f = useFacts(props)
  return (
    <table data-testid="overflow-status" className="mx-2 my-0.5 border-collapse text-[11px] text-text-muted">
      <tbody>
        <tr><th scope="row" className="pr-3 py-0.5 text-left font-normal">{f.label.sse}</th><td data-testid="overflow-sse" className="py-0.5 text-text-primary">{f.sseText}</td></tr>
        <tr><th scope="row" className="pr-3 py-0.5 text-left font-normal">{f.label.observers}</th><td data-testid="overflow-observers" className="py-0.5 text-text-primary tabular-nums">{props.observers}</td></tr>
        <tr><th scope="row" className="pr-3 py-0.5 text-left font-normal">{f.label.lease}</th><td data-testid="overflow-lease" className="py-0.5 text-text-primary font-mono break-all">{f.leaseFull}</td></tr>
      </tbody>
    </table>
  )
}
