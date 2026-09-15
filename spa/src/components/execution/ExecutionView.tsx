// spa/src/components/execution/ExecutionView.tsx — the {kind:'execution'}
// pane (spec §4.3.3). Composes the observe subscription, the lazy control
// lease, the pane's actions (useExecutionActions: send/interrupt/terminate,
// the only writes), the shared message renderer and StreamInput.
import { useCallback, useMemo } from 'react'
import ConversationMessages from '../ConversationMessages'
import StreamInput from '../StreamInput'
import ExecutionHeader from './ExecutionHeader'
import { useExecutionStore, executionKey } from '../../stores/useExecutionStore'
import { useExecutionSubscription } from '../../hooks/useExecutionSubscription'
import { useExecutionLease } from '../../hooks/useExecutionLease'
import { useExecutionActions } from '../../hooks/useExecutionActions'
import { useI18nStore } from '../../stores/useI18nStore'
import { getNexClientId } from '../../lib/nex/client-id'
import { defaultExecutionState } from '../../lib/nex/event-reducer'

export interface ExecutionViewProps { hostId: string; executionId: string; isActive: boolean }

const EMPTY = defaultExecutionState()
const TERMINAL_STATES = new Set(['rejected', 'failed', 'terminated'])
const KNOWN_ERROR_KEYS = new Set(['invalid_text', 'execution_archived', 'execution_terminal', 'turn_failed_to_launch', 'turn_stalled', 'interrupt_unconfirmed'])

export default function ExecutionView({ hostId, executionId, isActive }: ExecutionViewProps) {
  const t = useI18nStore((s) => s.t)
  const key = executionKey(hostId, executionId)
  const st = useExecutionStore((s) => s.executions[key] ?? EMPTY)
  const { problem } = useExecutionSubscription(hostId, executionId, isActive)
  const lease = useExecutionLease(hostId, executionId)
  const { draft, handleSend, handleInterrupt, handleTerminate } = useExecutionActions(hostId, executionId, lease)

  const isMine = useCallback((p: string | undefined) => !!p && p.endsWith(`/${getNexClientId()}`), [])
  const costUsd = useMemo(() => st.messages.reduce((sum, m) => sum + ((m as { total_cost_usd?: number }).total_cost_usd ?? 0), 0), [st.messages])

  if (problem) {
    const text = problem === 'not_found' ? t('execution.not_found')
      : problem === 'host_removed' ? t('execution.host_removed')
      : problem === 'nex_disabled' ? t('execution.nex_disabled')
      : t('execution.nex_unavailable', { error: st.sseError ?? '' })
    return <div data-testid="execution-problem" className="flex items-center justify-center h-full text-sm text-text-muted">{text}</div>
  }

  const terminal = !!st.summary && TERMINAL_STATES.has(st.summary.state)
  const ended = terminal || !!st.summary?.archived
  // The SSE handle can die terminally (401/403, or a non-retryable
  // structured error) after history has loaded, with no reconnect ever
  // coming — the pane looks live but a send would 2xx into the void with
  // no message_accepted/result ever arriving (Codex R4 P2). Gate input on
  // it same as `ended`; `sse` flips back off 'closed' the moment the pane
  // is reactivated (see useExecutionSubscription's activation effect), so
  // this clears itself without redesigning the reconnect path.
  const streamDead = st.historyLoaded && st.sse === 'closed' && !!st.sseError
  const placeholder = st.summary?.archived ? t('execution.input.archived')
    : ended ? t('execution.input.terminal')
    : streamDead ? t('execution.input.disconnected')
    : undefined
  const leaseHeld = st.leaseError?.code === 'lease_held'
  const errorText = st.sendError
    ? (KNOWN_ERROR_KEYS.has(st.sendError.code) ? t(`execution.error.${st.sendError.code}`) : t('execution.error.generic', { message: st.sendError.message }))
    : null

  return (
    <div className="flex flex-col h-full">
      <ExecutionHeader summary={st.summary} costUsd={costUsd} sse={st.sse} isMine={isMine}
        onInterrupt={() => void handleInterrupt()} onTerminate={() => void handleTerminate()} busy={terminal} />
      {!st.historyLoaded ? (
        <div data-testid="execution-loading" className="flex-1 flex flex-col items-center justify-center gap-1 text-sm text-text-muted">
          <span>{t('execution.loading')}</span>
          {st.sse === 'closed' && st.sseError && (
            <span data-testid="execution-loading-error" className="text-xs text-status-error">
              {t('execution.loading_error', { message: st.sseError })}
            </span>
          )}
        </div>
      ) : (
        <ConversationMessages messages={st.messages} keyPrefix={executionId} showThinking={st.pendingSend && st.pendingLocal?.delivery !== 'queued'}
          showEmptyHint={st.messages.length === 0 && !st.pendingLocal} emptyText={t('execution.empty')} scrollKey={st.pendingLocal ? 1 : 0}>
          {st.pendingLocal && (
            <div className="flex justify-end">
              <div className="flex items-center gap-2 bg-surface-input rounded-[12px_12px_4px_12px] px-3 py-1.5 text-sm">
                <span>{st.pendingLocal.text}</span>
                {st.pendingLocal.delivery === 'queued' && <span className="text-[10px] uppercase text-text-muted">{t('execution.queued')}</span>}
              </div>
            </div>
          )}
        </ConversationMessages>
      )}
      {leaseHeld && (
        <div data-testid="lease-held" className="mx-2 mb-1 text-xs text-status-warning">
          {t('execution.lease_held', { principal: st.leaseError?.heldBy ?? '' })}
        </div>
      )}
      {errorText && <div data-testid="send-error" className="mx-2 mb-1 text-xs text-status-error">{errorText}</div>}
      <StreamInput key={draft ?? ''} initialValue={draft ?? undefined} onSend={(text) => void handleSend(text)} showAttach={false}
        disabled={st.pendingSend || ended || !st.historyLoaded || streamDead} placeholder={placeholder} focused={isActive} />
    </div>
  )
}
