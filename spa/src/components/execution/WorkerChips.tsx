// spa/src/components/execution/WorkerChips.tsx — the worker's current facts as small chips on the right of the deck's status row (U4-4b; spec §3.5): the
// event stream's state, how many observers, who holds the control lease — shown as `● live · 2 observers · lease: you`. Read-only; one line, truncated when the row is narrow (the dot stays).
import { useI18nStore } from '../../stores/useI18nStore'
import { getNexClientId } from '../../lib/nex/client-id'
import type { ExecutionState } from '../../lib/nex/event-reducer'
import type { ExecutionLeaseView } from '../../lib/nex/types'

const SSE_DOT: Record<ExecutionState['sse'], string> = {
  open: 'bg-status-success',
  idle: 'bg-status-warning', connecting: 'bg-status-warning', reconnecting: 'bg-status-warning',
  closed: 'bg-status-error',
  paused: 'bg-text-muted',
}

/** The lease principal this client holds ends with its client id (the same rule the old worker dock used). */
const isMine = (principal: string | undefined) => !!principal && principal.endsWith(`/${getNexClientId()}`)

export function WorkerChips({ sse, observers, lease }: { sse: ExecutionState['sse']; observers: number; lease?: ExecutionLeaseView | null }) {
  const t = useI18nStore((s) => s.t)
  const leaseText = !lease ? t('execution.lease_none') : `${t('room.dock.lease')}: ${isMine(lease.principal_id) ? t('room.dock.you') : lease.principal_id}`
  return (
    <span data-testid="worker-chips" className="flex min-w-0 items-center gap-1.5 text-xs text-text-muted">
      <span data-testid="worker-chips-dot" className={`h-1.5 w-1.5 shrink-0 rounded-full ${SSE_DOT[sse]}`} />
      <span className="truncate">{t(`execution.sse.${sse === 'idle' ? 'connecting' : sse}`)} · {observers} {t('room.dock.observers')} · {leaseText}</span>
    </span>
  )
}
