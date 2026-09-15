// spa/src/components/execution/ExecutionView.tsx — the {kind:'execution'}
// pane (spec §4.3.3). Composes the observe subscription, the lazy control
// lease, the shared message renderer and StreamInput. Send/interrupt/
// terminate are the only writes; every one goes through ensureLease().
import { useCallback, useMemo, useState } from 'react'
import ConversationMessages from '../ConversationMessages'
import StreamInput from '../StreamInput'
import ExecutionHeader from './ExecutionHeader'
import { useExecutionStore, executionKey } from '../../stores/useExecutionStore'
import { useExecutionSubscription } from '../../hooks/useExecutionSubscription'
import { useExecutionLease } from '../../hooks/useExecutionLease'
import { useI18nStore } from '../../stores/useI18nStore'
import { getNexClientId } from '../../lib/nex/client-id'
import { interruptExecution, sendMessage, terminateExecution } from '../../lib/nex/nex-api'
import { NexApiError } from '../../lib/nex/types'
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
  const { ensureLease, touch } = useExecutionLease(hostId, executionId)
  const [draft, setDraft] = useState<string | null>(null) // restored text after a failed send

  const isMine = useCallback((p: string | undefined) => !!p && p.endsWith(`/${getNexClientId()}`), [])
  const costUsd = useMemo(() => st.messages.reduce((sum, m) => sum + ((m as { total_cost_usd?: number }).total_cost_usd ?? 0), 0), [st.messages])

  const store = () => useExecutionStore.getState()
  // A fetch that never reached the daemon rejects with a TypeError, not a
  // NexApiError; I12 still wants a code, so map it (`network`).
  const fail = useCallback((e: unknown) => {
    if (e instanceof NexApiError) {
      // no_live_turn / lease_abandoned are silent; lease_held is already
      // surfaced by the dedicated notice (ensureLease wrote leaseError
      // before rethrowing) — a second "Send failed" banner would be
      // redundant and there is no execution.error.lease_held copy for it.
      if (e.code === 'no_live_turn' || e.code === 'lease_abandoned' || e.code === 'lease_held') return
      store().setSendError(hostId, executionId, { code: e.code, message: e.message, turnId: e.turnId })
    } else {
      store().setSendError(hostId, executionId, { code: 'network', message: e instanceof Error ? e.message : String(e) })
    }
  }, [hostId, executionId])

  const handleSend = useCallback(async (text: string) => {
    store().setSendError(hostId, executionId, null)
    setDraft(null)
    touch()
    try {
      const leaseId = await ensureLease()
      store().setPendingLocal(hostId, executionId, { text, delivery: null })
      store().setPendingSend(hostId, executionId, true)
      const r = await sendMessage(hostId, executionId, leaseId, text)
      store().setPendingLocal(hostId, executionId, { text, delivery: r.delivery })
      store().setLastTurn(hostId, executionId, { turnId: r.turn_id, delivery: r.delivery })
    } catch (e) {
      store().setPendingLocal(hostId, executionId, null)
      store().setPendingSend(hostId, executionId, false)
      setDraft(text)
      fail(e)
    }
  }, [hostId, executionId, ensureLease, touch, fail])

  const handleInterrupt = useCallback(async () => {
    touch()
    try { await interruptExecution(hostId, executionId, await ensureLease()) } catch (e) { fail(e) }
  }, [hostId, executionId, ensureLease, touch, fail])

  const handleTerminate = useCallback(async () => {
    touch()
    try { await terminateExecution(hostId, executionId, await ensureLease()) } catch (e) { fail(e) }
  }, [hostId, executionId, ensureLease, touch, fail])

  if (problem) {
    const text = problem === 'not_found' ? t('execution.not_found')
      : problem === 'host_removed' ? t('execution.host_removed')
      : problem === 'nex_disabled' ? t('execution.nex_disabled')
      : t('execution.nex_unavailable', { error: st.sseError ?? '' })
    return <div data-testid="execution-problem" className="flex items-center justify-center h-full text-sm text-text-muted">{text}</div>
  }

  const terminal = !!st.summary && TERMINAL_STATES.has(st.summary.state)
  const ended = terminal || !!st.summary?.archived
  const placeholder = st.summary?.archived ? t('execution.input.archived') : ended ? t('execution.input.terminal') : undefined
  const leaseHeld = st.leaseError?.code === 'lease_held'
  const errorText = st.sendError
    ? (KNOWN_ERROR_KEYS.has(st.sendError.code) ? t(`execution.error.${st.sendError.code}`) : t('execution.error.generic', { message: st.sendError.message }))
    : null

  return (
    <div className="flex flex-col h-full">
      <ExecutionHeader summary={st.summary} costUsd={costUsd} sse={st.sse} isMine={isMine}
        onInterrupt={() => void handleInterrupt()} onTerminate={() => void handleTerminate()} busy={terminal} />
      {!st.historyLoaded ? (
        <div data-testid="execution-loading" className="flex-1 flex items-center justify-center text-sm text-text-muted">{t('execution.loading')}</div>
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
        disabled={st.pendingSend || ended || !st.historyLoaded} placeholder={placeholder} focused={isActive} />
    </div>
  )
}
