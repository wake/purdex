// spa/src/hooks/useWorkerHandoff.ts — the controls that hand a worker over or end it: 帶回終端機 (take back to the session it came from, or to a new terminal)
// and 退出 (exit), with their busy flags, the running-turn confirms, and the status bar's terminal button (`registerTakeToTerminal`). The worker pane
// (WorkerDeckPane, as deck or chat) calls it; one worker view is mounted at a time.
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'
import { useI18nStore } from '../stores/useI18nStore'
import { executionKey, useExecutionStore } from '../stores/useExecutionStore'
import type { ExecutionState } from '../lib/nex/event-reducer'
import { useUndoToast } from '../stores/useUndoToast'
import { isLiveRow } from '../lib/nex/live-workers'
import { takeBack, takeToTerminal, handoffErrorMessage, manualResumeHint } from '../lib/nex/handoff'
import { HandoffApiError } from '../lib/nex/handoff-api'
import { announceTakeOutcome } from '../lib/nex/take-outcome'
import { exitWorker, exitErrorMessage } from '../lib/nex/exit-worker'
import { registerTakeToTerminal } from '../lib/nex/take-to-terminal-registry'
import type { ExecutionFrom } from '../types/tab'
import type { ExecutionLeaseApi } from './useExecutionLease'

/**
 * States the daemon's take-to-terminal can settle (spec §4.2): never `queued`
 * (it would answer `execution_not_settled`) nor `rejected`.
 */
const TAKEABLE_STATES = new Set(['running', 'idle', 'failed', 'terminated'])

export interface WorkerHandoffInput {
  hostId: string
  executionId: string
  tabId: string
  paneId: string
  /** Set when the execution was handed off from a tmux session; take-back then returns to that session. */
  from?: ExecutionFrom
  lease: Pick<ExecutionLeaseApi, 'forget'>
  st: ExecutionState
  /** The view shows a problem instead of the worker (no header, no confirm dialog): the terminal button is not offered then. */
  problem: unknown
  /** An interrupt / reply-box write is on its way (the deck's send queue). */
  actionPending: boolean
}

export function useWorkerHandoff({ hostId, executionId, tabId, paneId, from, lease, st, problem, actionPending }: WorkerHandoffInput) {
  const t = useI18nStore((s) => s.t)
  const key = executionKey(hostId, executionId)
  // Take-back: `takeBack` is single-flight per execution, but the busy flag
  // is what the header shows; the ref keeps a same-tick second click from
  // reaching it before React commits the state. While it is pending every
  // write to the execution (send / interrupt / exit) is frozen too: the
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
      const { result, swapped } = from
        ? await takeBack({ ...common, from })
        : await takeToTerminal({ ...common, cwd: cwd! })
      announceTakeOutcome(t, { result, swapped })
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
  // 退出: single-flight in `exitWorker`; the ref stops a same-tick second click.
  const [exitBusy, setExitBusy] = useState(false)
  const exitInFlight = useRef(false)
  const [confirmExit, setConfirmExit] = useState(false)
  const runExit = useCallback(async () => {
    if (exitInFlight.current) return
    exitInFlight.current = true
    setExitBusy(true)
    try {
      const result = await exitWorker({ hostId, executionId, leaseId: useExecutionStore.getState().executions[key]?.lease?.leaseId, forgetLease: lease.forget })
      // Patch the summary at once from the result (the SSE confirms later): no longer live,
      // so the pane turns into WorkerEndedPane.
      useExecutionStore.getState().applySummaryPatch(hostId, executionId, { state: result.state, archived: result.archived })
    } catch (err) {
      useUndoToast.getState().show(exitErrorMessage(err, t))
    } finally {
      exitInFlight.current = false
      setExitBusy(false)
    }
  }, [hostId, executionId, key, lease.forget, t])
  const onExit = useCallback(() => {
    if (exitInFlight.current) return
    if (useExecutionStore.getState().executions[key]?.summary?.state === 'running') setConfirmExit(true)
    else void runExit()
  }, [key, runExit])
  const exitable = !!st.summary && isLiveRow(st.summary)
  // A write already on its way to the daemon (send, interrupt)
  // could land after the daemon's settled check and before its archive;
  // the daemon re-verifies (#1171), and the SPA refuses to start the race.
  const writeInFlight = st.pendingSend || actionPending
  // 退出 is held back only by a write still on its way: the unaccepted send (`sendLocked`) or an interrupt.
  // `pendingSend` lasts until the turn's result, so it would lock 退出 for the whole turn this pane sent —
  // a worker waiting for approval included (permission channel §5.4 / N7: that exit cancels the request).
  // A running turn is exitable after the confirm, as from the Workers list.
  const exitWriteInFlight = st.sendLocked || actionPending
  const onTakeBack = useCallback(() => {
    if (takeBackInFlight.current || exitInFlight.current || writeInFlight) return
    if (useExecutionStore.getState().executions[key]?.summary?.state === 'running') setConfirmTakeBack(true)
    else void runTakeBack()
  }, [key, runTakeBack, writeInFlight])
  // Spec §4.2: every claude execution with a session id can go to a terminal
  // — a fresh one when there is no origin session to return to.
  // Archived is excluded too: the daemon refuses it (`execution_archived`) —
  // whoever archived it already resumed that transcript elsewhere.
  const canTakeToTerminal = !from && !!st.summary && st.summary.provider === 'claude' && !st.summary.archived
    && !!(st.summary.session_id || st.summary.resume_session_id) && TAKEABLE_STATES.has(st.summary.state)
  // Shell cleanup §9.5: the status bar's terminal button runs this same flow
  // (confirm, lease forget, busy guards) through the registry. Offered only
  // where the header offers it — a problem pane has no header (and no confirm
  // dialog) — and busy under the header's own busy condition. The entry calls
  // through a ref, so a re-render that leaves canTake / busy alone still runs
  // the latest closure without re-registering.
  const takeOffered = !problem && (!!from || canTakeToTerminal)
  const takeBusy = takeBackBusy || exitBusy || writeInFlight
  const onTakeBackRef = useRef(onTakeBack)
  useLayoutEffect(() => { onTakeBackRef.current = onTakeBack })
  useEffect(() => registerTakeToTerminal(paneId, {
    canTake: takeOffered, busy: takeBusy, takeToTerminal: () => onTakeBackRef.current(),
  }), [paneId, takeOffered, takeBusy])
  return { takeBackBusy, exitBusy, confirmTakeBack, setConfirmTakeBack, confirmExit, setConfirmExit, runTakeBack, runExit, onTakeBack, onExit, exitable, exitWriteInFlight, takeOffered, takeBusy }
}
