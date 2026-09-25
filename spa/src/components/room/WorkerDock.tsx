// spa/src/components/room/WorkerDock.tsx — the worker's "current state" strip
// (worker pane spec §4.6), rendered inside the pane (user, Q3) between the
// transcript and the input. It carries what is *current* rather than
// chronological: the SSE state, the observer count and the lease holder —
// the facts the slimmed header (§4.7) gave up. Collapsed it is one row
// (`● live · 2 observers · lease: you`); expanded it is a small table with
// one row per fact. The open flag is local: the dock is not transcript
// content, so it is not part of the turn fold store.
// Background shells (§4.6) need wire ask A1 and are not in R1; there is no
// `shells` prop yet — R4 adds one rather than reshaping this component.
import { useState } from 'react'
import { CaretDown, CaretRight } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import type { ExecutionState } from '../../lib/nex/event-reducer'
import type { ExecutionLeaseView } from '../../lib/nex/types'

export interface WorkerDockProps {
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

export default function WorkerDock({ sse, observers, lease, isMine }: WorkerDockProps) {
  const t = useI18nStore((s) => s.t)
  const [expanded, setExpanded] = useState(false)
  const sseText = t(`execution.sse.${sse === 'idle' ? 'connecting' : sse}`)
  const mine = !!lease && isMine(lease.principal_id)
  const leaseShort = !lease ? t('execution.lease_none')
    : `${t('room.dock.lease')}: ${mine ? t('room.dock.you') : lease.principal_id}`
  const leaseFull = !lease ? t('execution.lease_none')
    : `${lease.principal_id}${mine ? ` ${t('execution.lease_you')}` : ''}`
  const Caret = expanded ? CaretDown : CaretRight

  return (
    <div data-testid="worker-dock" className="shrink-0 border-t border-border-subtle px-4 py-1 text-xs text-text-muted">
      <div className="flex items-center gap-2 min-w-0">
        <button type="button" data-testid="worker-dock-toggle" aria-expanded={expanded}
          aria-label={t(expanded ? 'room.dock.collapse' : 'room.dock.expand')}
          onClick={() => setExpanded((v) => !v)}
          className="shrink-0 rounded p-0.5 hover:bg-surface-hover hover:text-text-primary">
          <Caret size={10} />
        </button>
        <span data-testid="worker-dock-dot" className={`shrink-0 w-1.5 h-1.5 rounded-full ${SSE_DOT[sse]}`} />
        {!expanded && (
          <span data-testid="worker-dock-row" className="truncate min-w-0">
            {sseText} · {observers} {t('room.dock.observers')} · {leaseShort}
          </span>
        )}
      </div>
      {expanded && (
        <table data-testid="worker-dock-table" className="ml-6 my-0.5 border-collapse">
          <tbody>
            <tr>
              <th scope="row" className="pr-3 py-0.5 text-left font-normal">{t('room.dock.sse')}</th>
              <td className="py-0.5 text-text-primary">{sseText}</td>
            </tr>
            <tr>
              <th scope="row" className="pr-3 py-0.5 text-left font-normal">{t('room.dock.observers')}</th>
              <td className="py-0.5 text-text-primary tabular-nums">{observers}</td>
            </tr>
            <tr>
              <th scope="row" className="pr-3 py-0.5 text-left font-normal">{t('room.dock.lease')}</th>
              <td className="py-0.5 text-text-primary font-mono break-all">{leaseFull}</td>
            </tr>
          </tbody>
        </table>
      )}
    </div>
  )
}
