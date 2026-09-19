// spa/src/components/execution/ExecutionView.tsx — the {kind:'execution'}
// pane (spec §4.3.3). Composes the observe subscription, the lazy control
// lease, the pane's actions (useExecutionActions: send/interrupt/terminate,
// the only writes), the shared message renderer and StreamInput. It also
// owns "Take to terminal": confirm when a turn is running, then
// `lib/nex/handoff.ts` does the request, the lease forget and the pane swap
// (which unmounts this view) — `takeBack` to the origin session when the
// execution came from one (`from`, P-C.3 spec §4.4), else `takeToTerminal`
// into a fresh session in the execution's cwd (exec-to-terminal spec §4.2).
import { useCallback, useMemo, useRef, useState } from 'react'
import ConversationMessages from '../ConversationMessages'
import StreamInput from '../StreamInput'
import ExecutionHeader from './ExecutionHeader'
import { ConfirmDialog } from '../ConfirmDialog'
import { useExecutionStore, executionKey } from '../../stores/useExecutionStore'
import { useUndoToast } from '../../stores/useUndoToast'
import { useExecutionSubscription } from '../../hooks/useExecutionSubscription'
import { useExecutionLease } from '../../hooks/useExecutionLease'
import { useExecutionActions } from '../../hooks/useExecutionActions'
import { useElapsedTicker } from '../../hooks/useElapsedTicker'
import { useI18nStore } from '../../stores/useI18nStore'
import { getNexClientId } from '../../lib/nex/client-id'
import { defaultExecutionState } from '../../lib/nex/event-reducer'
import { costSummary } from '../../lib/nex/cost-summary'
import { partialHasVisibleContent } from '../../lib/nex/partial'
import { HandoffApiError } from '../../lib/nex/handoff-api'
import { takeBack, takeToTerminal, handoffErrorMessage, manualResumeHint } from '../../lib/nex/handoff'
import type { ExecutionFrom } from '../../types/tab'

export interface ExecutionViewProps {
  hostId: string
  executionId: string
  isActive: boolean
  /** The pane this view lives in — the take-back swaps its content. */
  tabId: string
  paneId: string
  /** Set when the execution was handed off from a tmux session; "Take to terminal" then returns to that session. */
  from?: ExecutionFrom
}

const EMPTY = defaultExecutionState()
const TERMINAL_STATES = new Set(['rejected', 'failed', 'terminated'])
/**
 * States the daemon's take-to-terminal can settle (spec §4.2): never `queued`
 * (it would answer `execution_not_settled`) nor `rejected`.
 */
const TAKEABLE_STATES = new Set(['running', 'idle', 'failed', 'terminated'])
const KNOWN_ERROR_KEYS = new Set(['invalid_text', 'execution_archived', 'execution_terminal', 'turn_failed_to_launch', 'turn_stalled', 'interrupt_unconfirmed'])

export default function ExecutionView({ hostId, executionId, isActive, tabId, paneId, from }: ExecutionViewProps) {
  const t = useI18nStore((s) => s.t)
  const key = executionKey(hostId, executionId)
  const st = useExecutionStore((s) => s.executions[key] ?? EMPTY)
  const { problem } = useExecutionSubscription(hostId, executionId, isActive)
  const lease = useExecutionLease(hostId, executionId)
  const { draft, actionPending, handleSend, handleInterrupt, handleTerminate } = useExecutionActions(hostId, executionId, lease)

  // Take-back: `takeBack` is single-flight per execution, but the busy flag
  // is what the header shows; the ref keeps a same-tick second click from
  // reaching it before React commits the state. While it is pending every
  // write to the execution (send / interrupt / terminate) is frozen too: the
  // daemon is interrupting and archiving it, and a write racing that would
  // land on an execution that is about to be gone.
  const [takeBackBusy, setTakeBackBusy] = useState(false)
  const takeBackInFlight = useRef(false)
  const [confirmTakeBack, setConfirmTakeBack] = useState(false)
  const runTakeBack = useCallback(async () => {
    if (takeBackInFlight.current) return
    const exec = useExecutionStore.getState().executions[key]
    // Without `from` the request needs the execution's cwd; the control is
    // only offered once the summary is in, so this is a same-tick race guard.
    const cwd = from ? undefined : exec?.summary?.cwd
    if (!from && !cwd) return
    takeBackInFlight.current = true
    setTakeBackBusy(true)
    const toast = useUndoToast.getState()
    try {
      const leaseId = exec?.lease?.leaseId
      const common = { hostId, executionId, leaseId, tabId, paneId, forgetLease: lease.forget }
      const { swapped } = from
        ? await takeBack({ ...common, from })
        : await takeToTerminal({ ...common, cwd: cwd! })
      // On `swapped` this view is already unmounted (the pane is a terminal
      // again); the toast is global, so it still lands.
      toast.show(swapped ? t('takeback.success') : t('takeback.archived_no_pane'))
    } catch (err) {
      if (err instanceof HandoffApiError) {
        const id = manualResumeHint(err)
        const message = handoffErrorMessage(t, err)
        toast.show(id ? `${message}\n${t('takeback.manual_resume', { id })}` : message)
      } else {
        toast.show(t('handoff.error.generic', { code: 'unknown' }))
      }
    } finally {
      takeBackInFlight.current = false
      setTakeBackBusy(false)
    }
  }, [from, hostId, executionId, key, tabId, paneId, lease.forget, t])
  // A write already on its way to the daemon (send, interrupt, terminate)
  // could land after the daemon's settled check and before its archive;
  // the daemon re-verifies (#1171), and the SPA refuses to start the race.
  const writeInFlight = st.pendingSend || actionPending
  const onTakeBack = useCallback(() => {
    if (takeBackInFlight.current || writeInFlight) return
    if (useExecutionStore.getState().executions[key]?.summary?.state === 'running') setConfirmTakeBack(true)
    else void runTakeBack()
  }, [key, runTakeBack, writeInFlight])

  const isMine = useCallback((p: string | undefined) => !!p && p.endsWith(`/${getNexClientId()}`), [])
  // P-B4 spec §4.2: null until history is loaded so the header shows `$…`
  // rather than a partial sum.
  const cost = useMemo(() => (st.historyLoaded ? costSummary(st.messages) : null), [st.messages, st.historyLoaded])
  // Spec §4.2: the 1 s clock only runs while some tool is running.
  const anyRunning = useMemo(() => Object.values(st.tools).some((tool) => tool.status === 'running'), [st.tools])
  const now = useElapsedTicker(anyRunning)

  if (problem) {
    const text = problem === 'not_found' ? t('execution.not_found')
      : problem === 'host_removed' ? t('execution.host_removed')
      : problem === 'nex_disabled' ? t('execution.nex_disabled')
      : t('execution.nex_unavailable', { error: st.sseError ?? '' })
    return <div data-testid="execution-problem" className="flex items-center justify-center h-full text-sm text-text-muted">{text}</div>
  }

  const terminal = !!st.summary && TERMINAL_STATES.has(st.summary.state)
  const ended = terminal || !!st.summary?.archived
  // Spec §4.2: every claude execution with a session id can go to a terminal
  // — a fresh one when there is no origin session to return to.
  // Archived is excluded too: the daemon refuses it (`execution_archived`) —
  // whoever archived it already resumed that transcript elsewhere.
  const canTakeToTerminal = !from && !!st.summary && st.summary.provider === 'claude' && !st.summary.archived
    && !!(st.summary.session_id || st.summary.resume_session_id) && TAKEABLE_STATES.has(st.summary.state)
  // The SSE handle can die terminally (401/403, or a non-retryable
  // structured error) after history has loaded, with no reconnect ever
  // coming — the pane looks live but a send would 2xx into the void with
  // no message_accepted/result ever arriving. Gate input on
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
  // Spec §4.4 R3: dots while the model is silent (own delivered send, or an
  // observed live turn); the typewriter takes over once tokens flow, and a
  // running tool's spinner already shows activity, so no dots beside it.
  const showThinking = (st.turnLive || (st.pendingSend && st.pendingLocal?.delivery !== 'queued'))
    && !partialHasVisibleContent(st.partial) && !anyRunning

  return (
    <div className="flex flex-col h-full">
      <ExecutionHeader summary={st.summary} cost={cost} hostId={hostId} sse={st.sse} isMine={isMine}
        onInterrupt={() => void handleInterrupt()} onTerminate={() => void handleTerminate()} busy={terminal || takeBackBusy}
        onTakeBack={from || canTakeToTerminal ? onTakeBack : undefined} takeBackBusy={takeBackBusy || writeInFlight} />
      {confirmTakeBack && (
        <ConfirmDialog testIdPrefix="takeback" title={t('takeback.confirm_title')} body={t('takeback.confirm_running')}
          confirmLabel={t('takeback.button')} onCancel={() => setConfirmTakeBack(false)}
          onConfirm={() => { setConfirmTakeBack(false); void runTakeBack() }} />
      )}
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
        <ConversationMessages messages={st.messages} keyPrefix={executionId} showThinking={showThinking}
          showEmptyHint={st.messages.length === 0 && !st.pendingLocal} emptyText={t('execution.empty')} scrollKey={st.pendingLocal ? 1 : 0}
          partial={st.partial} tools={st.tools} now={now}>
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
        disabled={st.pendingSend || ended || !st.historyLoaded || streamDead || takeBackBusy} placeholder={placeholder} focused={isActive} />
    </div>
  )
}
