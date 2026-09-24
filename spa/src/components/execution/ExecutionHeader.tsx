// spa/src/components/execution/ExecutionHeader.tsx — the facts strip above
// an execution conversation (spec §4.3.3): state, provider/profile, cwd,
// observers, lease holder, turns, cost, SSE status, the two lease-backed
// actions, and "Take to terminal" (P-C.3 spec §4.4 for the session-bound
// path, exec-to-terminal spec §4.2 for every other claude execution). Pure
// presentation; ExecutionView owns the network and decides when the control
// is offered. The cost is an anchor button with a one-line hover summary
// (P-B4 spec §4.2 H1–H2); `cost === null` means history is not loaded yet.
// The button is `aria-describedby` the tooltip, and both render the total
// through the same `formatUsd` so they can never disagree on a value. Click
// toggles the `CostPanel` (H3): the header owns the open flag and the anchor
// ref, and `FloatingPanel` handles Escape / outside-click through that ref.
import { useEffect, useId, useRef, useState } from 'react'
import { ArrowUUpLeft, Prohibit, Power } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { HoverTooltip } from '../HoverTooltip'
import CostPanel from './CostPanel'
import type { ExecutionSummary } from '../../lib/nex/types'
import type { ExecutionState } from '../../lib/nex/event-reducer'
import type { CostSummary } from '../../lib/nex/cost-summary'
import { formatTokens, formatUsd } from '../../lib/nex/format-cost'
import { formatDuration } from '../../lib/nex/format-duration'

export interface ExecutionHeaderProps {
  summary: ExecutionSummary | null
  /** `costSummary(messages)` once history is loaded, else null (`$…`, disabled). */
  cost: CostSummary | null
  /** Forwarded to `CostPanel` (its quota row, P-B4 P5). */
  hostId: string
  sse: ExecutionState['sse']
  isMine: (principal: string | undefined) => boolean
  onInterrupt: () => void
  onTerminate: () => void
  /** Gates interrupt/terminate (terminal execution, or a take-back in flight). Take-back has its own flag. */
  busy: boolean
  /** Present when the execution can be taken to a terminal (ExecutionView decides). */
  onTakeBack?: () => void
  takeBackBusy?: boolean
}

const STATE_DOT: Record<string, string> = {
  running: 'bg-status-success', idle: 'bg-text-muted', queued: 'bg-status-warning',
  failed: 'bg-status-error', rejected: 'bg-status-error', terminated: 'bg-status-error',
}

export const TERMINATE_CONFIRM_MS = 4000

export default function ExecutionHeader({ summary, cost, hostId, sse, isMine, onInterrupt, onTerminate, busy, onTakeBack, takeBackBusy = false }: ExecutionHeaderProps) {
  const t = useI18nStore((s) => s.t)
  const [confirming, setConfirming] = useState(false)
  const [costOpen, setCostOpen] = useState(false)
  const costRef = useRef<HTMLButtonElement>(null)
  const costTipId = useId()
  useEffect(() => {
    if (!confirming) return
    const id = setTimeout(() => setConfirming(false), TERMINATE_CONFIRM_MS)
    return () => clearTimeout(id)
  }, [confirming])

  const state = summary?.state ?? '…'
  const cwdBase = summary?.cwd ? summary.cwd.split('/').filter(Boolean).pop() ?? summary.cwd : ''
  const lease = summary?.lease
  const leaseText = lease ? `${lease.principal_id}${isMine(lease.principal_id) ? ` ${t('execution.lease_you')}` : ''}` : t('execution.lease_none')
  // H2: the tooltip counts what it summed (`turns.length`, not summary.turn_count).
  const costLine = cost ? t('execution.cost.summary', {
    turns: cost.turns.length, usd: formatUsd(cost.totalUsd), out: formatTokens(cost.tokens.output),
    api: formatDuration(cost.apiMs), wall: formatDuration(cost.durationMs),
  }) : ''

  return (
    <div className="flex flex-col gap-1 px-4 py-2 border-b border-border-default text-xs text-text-muted">
      <div className="flex items-center gap-2">
        <span className={`w-2 h-2 rounded-full ${STATE_DOT[state] ?? 'bg-text-muted'}`} />
        <span data-testid="execution-state" className="text-text-primary font-medium">{state}</span>
        {summary && <span>{summary.provider} · {summary.effective_profile ?? summary.requested_profile ?? '—'}</span>}
        {cwdBase && <span title={summary?.cwd} className="font-mono">{cwdBase}</span>}
        <div className="flex-1" />
        <span data-testid="execution-sse">{t(`execution.sse.${sse === 'idle' ? 'connecting' : sse}`)}</span>
      </div>
      <div className="flex items-center gap-3">
        <span>{summary?.observers ?? 0} {t('execution.observers')}</span>
        <span data-testid="execution-lease">{leaseText}</span>
        {summary?.turn_count != null && <span>{summary.turn_count} {t('execution.turns')}</span>}
        <button type="button" data-testid="execution-cost" disabled={!cost} ref={costRef}
          aria-describedby={cost && !costOpen ? costTipId : undefined}
          aria-expanded={cost ? costOpen : undefined}
          onClick={() => setCostOpen((v) => !v)}
          className="relative tabular-nums hover:underline disabled:no-underline disabled:cursor-default">
          {cost ? formatUsd(cost.totalUsd, 2) : t('execution.cost.loading')}
          {cost && !costOpen && <HoverTooltip id={costTipId} placement="top">{costLine}</HoverTooltip>}
        </button>
        {cost && costOpen && <CostPanel summary={cost} hostId={hostId} anchorRef={costRef} onClose={() => setCostOpen(false)} />}
        <div className="flex-1" />
        {onTakeBack && (
          <button type="button" data-testid="take-back" disabled={takeBackBusy} onClick={onTakeBack}
            className="flex items-center gap-1 px-2 py-0.5 rounded hover:bg-surface-hover disabled:opacity-40">
            <ArrowUUpLeft size={12} /> {t('takeback.button')}
          </button>
        )}
        <button type="button" disabled={busy} onClick={onInterrupt}
          className="flex items-center gap-1 px-2 py-0.5 rounded hover:bg-surface-hover disabled:opacity-40">
          <Prohibit size={12} /> {t('execution.interrupt')}
        </button>
        <button type="button" disabled={busy}
          onClick={() => { if (confirming) { setConfirming(false); onTerminate() } else setConfirming(true) }}
          className={`flex items-center gap-1 px-2 py-0.5 rounded hover:bg-surface-hover disabled:opacity-40 ${confirming ? 'text-status-error' : ''}`}>
          <Power size={12} /> {confirming ? t('execution.terminate_confirm') : t('execution.terminate')}
        </button>
      </div>
    </div>
  )
}
