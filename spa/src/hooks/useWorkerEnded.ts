// spa/src/hooks/useWorkerEnded.ts — what a worker whose conversation is over can do: rebuild it in a terminal (take to terminal) or as a new worker
// (worker-rebuild), or exit a worker that failed to start. Moved out of WorkerEndedPane unchanged (U4-4a): the ended screen and the deck's ended
// footer both call it. Worker is preselected (D12); a mode that cannot work is disabled and never left selected.
import { useRef, useState } from 'react'
import { useI18nStore } from '../stores/useI18nStore'
import { useNexHostStore, selectHandoffReady } from '../stores/useNexHostStore'
import { isLiveRow } from '../lib/nex/live-workers'
import { takeToTerminal, handoffErrorMessage } from '../lib/nex/handoff'
import { HandoffApiError } from '../lib/nex/handoff-api'
import { rebuildAsWorker, rebuildErrorMessage, announceRebuildOutcome } from '../lib/nex/worker-rebuild'
import { exitWorker, exitErrorMessage } from '../lib/nex/exit-worker'
import { announceTakeOutcome } from '../lib/nex/take-outcome'
import { workerEndedKind } from '../lib/nex/worker-ended'
import { useExecutionStore } from '../stores/useExecutionStore'
import type { RebuildMode } from '../components/RebuildModeChoice'
import type { ExecutionSummary } from '../lib/nex/types'

export interface WorkerEndedProps {
  hostId: string
  executionId: string
  summary: ExecutionSummary
  tabId: string
  paneId: string
}

export function useWorkerEnded({ hostId, executionId, summary, tabId, paneId }: WorkerEndedProps) {
  const t = useI18nStore((s) => s.t)
  const handoffReady = useNexHostStore(selectHandoffReady(hostId))
  const [choice, setChoice] = useState<RebuildMode>('worker')
  const [busy, setBusy] = useState(false)
  // What failed last: a rebuild (in the mode it was tried in) or the exit.
  const [error, setError] = useState<{ message: string; from: 'rebuild' | 'exit' } | null>(null)
  const inFlight = useRef(false)
  const [exiting, setExiting] = useState(false)
  const exitInFlight = useRef(false)

  const kind = workerEndedKind(summary) ?? 'exited'
  const sid = summary.session_id || summary.resume_session_id || ''
  const cwd = summary.cwd
  const terminalAvailable = summary.provider === 'claude' && !!sid && !!cwd
  const workerAvailable = handoffReady && !!sid && !!cwd
  // The selected option is never a disabled one: fall back to the one that works.
  const mode: RebuildMode = choice === 'worker'
    ? (workerAvailable || !terminalAvailable ? 'worker' : 'terminal')
    : (terminalAvailable || !workerAvailable ? 'terminal' : 'worker')
  const nothingAvailable = !terminalAvailable && !workerAvailable
  // A rebuild error belongs to the mode it was tried in; an exit error is not about the mode. Any change of the
  // effective mode clears it — a toggle, or the fallback when a mode stops working (Nexen readiness dropped, #1627 F),
  // so it does not come back with that mode. Render-time, so it never draws under the new mode.
  const [lastMode, setLastMode] = useState(mode)
  if (mode !== lastMode) {
    setLastMode(mode)
    setError((e) => (e?.from === 'rebuild' ? null : e))
  }

  const rebuild = async () => {
    if (inFlight.current || exitInFlight.current || nothingAvailable) return
    inFlight.current = true
    setBusy(true)
    setError(null)
    try {
      if (mode === 'terminal') {
        announceTakeOutcome(t, await takeToTerminal({ hostId, executionId, cwd, tabId, paneId, forgetLease: () => {} }))
      } else {
        const outcome = await rebuildAsWorker({
          hostId, sessionId: sid, cwd, tabId, paneId,
          profile: summary.effective_profile,
          replaceExecutionId: isLiveRow(summary) ? executionId : undefined,
          expect: (c) => c.kind === 'execution' && c.executionId === executionId && (c.host ?? hostId) === hostId,
        })
        announceRebuildOutcome(t, hostId, outcome)
      }
    } catch (err) {
      const message = mode === 'terminal' && err instanceof HandoffApiError
        ? (err.code === 'session_owned' ? rebuildErrorMessage(err, t) : t('worker.rebuild.failed', { reason: handoffErrorMessage(t, err) }))
        : rebuildErrorMessage(err, t)
      setError({ message, from: 'rebuild' })
    } finally {
      inFlight.current = false
      setBusy(false)
    }
  }

  // 退出 on a failed stint (spec §5/§6): archive only, no confirm, no lease (the daemon borrows, D4).
  const exit = async () => {
    if (exitInFlight.current || inFlight.current) return
    exitInFlight.current = true
    setExiting(true)
    setError(null)
    try {
      const result = await exitWorker({ hostId, executionId })
      // As ExecutionView.runExit: patch at once, the SSE confirms later; this pane turns into "exited".
      useExecutionStore.getState().applySummaryPatch(hostId, executionId, { state: result.state, archived: result.archived })
    } catch (err) {
      setError({ message: exitErrorMessage(err, t), from: 'exit' })
    } finally {
      exitInFlight.current = false
      setExiting(false)
    }
  }

  const reason = kind === 'failed' ? (summary.reject_reason || summary.terminal_reason) : undefined
  return { kind, reason, mode, setChoice, terminalAvailable, workerAvailable, nothingAvailable, busy, exiting, error, rebuild, exit }
}
