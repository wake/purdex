// spa/src/components/execution/WorkerEndedPane.tsx — what an execution pane shows
// once its worker has exited, or failed to start (conversation entity spec §6, §7):
// a dedicated screen from which the user rebuilds the conversation either in a
// terminal (take to terminal) or as a new worker (worker-rebuild). Worker is
// preselected (D12); a mode that cannot work is disabled and never left selected.
import { useRef, useState } from 'react'
import { SmileySad } from '@phosphor-icons/react'
import { RebuildScreen } from '../RebuildScreen'
import { RebuildModeChoice, type RebuildMode } from '../RebuildModeChoice'
import { useI18nStore } from '../../stores/useI18nStore'
import { useNexHostStore, selectHandoffReady } from '../../stores/useNexHostStore'
import { closeTab } from '../../lib/tab-lifecycle'
import { isLiveRow } from '../../lib/nex/live-workers'
import { takeToTerminal, handoffErrorMessage } from '../../lib/nex/handoff'
import { HandoffApiError } from '../../lib/nex/handoff-api'
import { rebuildAsWorker, rebuildErrorMessage } from '../../lib/nex/worker-rebuild'
import { exitWorker, exitErrorMessage } from '../../lib/nex/exit-worker'
import { useExecutionStore } from '../../stores/useExecutionStore'
import type { ExecutionSummary } from '../../lib/nex/types'

export type WorkerEndedKind = 'exited' | 'failed'

/** Not live → exited; live but failed / rejected → failed to start; anything else is a running pane (null). */
// eslint-disable-next-line react-refresh/only-export-components
export function workerEndedKind(s: ExecutionSummary | null): WorkerEndedKind | null {
  if (!s) return null
  if (!isLiveRow(s)) return 'exited'
  return s.state === 'failed' || s.state === 'rejected' ? 'failed' : null
}

interface Props {
  hostId: string
  executionId: string
  summary: ExecutionSummary
  tabId: string
  paneId: string
}

export function WorkerEndedPane({ hostId, executionId, summary, tabId, paneId }: Props) {
  const t = useI18nStore((s) => s.t)
  const handoffReady = useNexHostStore(selectHandoffReady(hostId))
  const [choice, setChoice] = useState<RebuildMode>('worker')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
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

  const rebuild = async () => {
    if (inFlight.current || exitInFlight.current || nothingAvailable) return
    inFlight.current = true
    setBusy(true)
    setError(null)
    try {
      if (mode === 'terminal') {
        await takeToTerminal({ hostId, executionId, cwd, tabId, paneId, forgetLease: () => {} })
      } else {
        await rebuildAsWorker({
          hostId, sessionId: sid, cwd, tabId, paneId,
          profile: summary.effective_profile,
          replaceExecutionId: isLiveRow(summary) ? executionId : undefined,
          expect: (c) => c.kind === 'execution' && c.executionId === executionId && (c.host ?? hostId) === hostId,
        })
      }
    } catch (err) {
      setError(mode === 'terminal' && err instanceof HandoffApiError
        ? (err.code === 'session_owned' ? rebuildErrorMessage(err, t) : t('worker.rebuild.failed', { reason: handoffErrorMessage(t, err) }))
        : rebuildErrorMessage(err, t))
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
      setError(exitErrorMessage(err, t))
    } finally {
      exitInFlight.current = false
      setExiting(false)
    }
  }

  const reason = kind === 'failed' ? (summary.reject_reason || summary.terminal_reason) : undefined

  return (
    <RebuildScreen
      testId="worker-ended"
      icon={<SmileySad size={48} className="text-zinc-500 mb-4" />}
      title={t(kind === 'failed' ? 'worker.ended.failed_title' : 'worker.ended.exited_title')}
      description={nothingAvailable
        ? t('worker.rebuild.worker_unavailable')
        : kind === 'exited' ? t('worker.ended.exited_desc') : undefined}
      detail={reason ? <p className="text-sm text-zinc-500 mb-6">{t('worker.ended.reason', { reason })}</p> : undefined}
      closeLabel={t('worker.ended.close_tab')}
      onClose={() => closeTab(tabId)}
    >
      <div className="flex flex-col items-center gap-3 w-full max-w-lg">
        <RebuildModeChoice
          value={mode}
          onChange={setChoice}
          terminalAvailable={terminalAvailable}
          workerAvailable={workerAvailable}
          workerUnavailableHint={t('worker.rebuild.worker_unavailable')}
        />
        <button
          type="button"
          data-testid="worker-rebuild"
          disabled={busy || exiting || nothingAvailable}
          onClick={() => { void rebuild() }}
          className="px-4 py-1.5 text-sm rounded bg-zinc-700 text-zinc-100 hover:bg-zinc-600 disabled:opacity-40 disabled:cursor-not-allowed"
        >
          {t('worker.rebuild.button')}
        </button>
        {kind === 'failed' && (
          <button
            type="button"
            data-testid="worker-ended-exit"
            disabled={exiting || busy}
            onClick={() => { void exit() }}
            className="text-sm text-zinc-400 hover:text-zinc-200 disabled:opacity-40 disabled:cursor-not-allowed"
          >
            {t('worker.exit.button')}
          </button>
        )}
        {error && <p data-testid="worker-rebuild-error" role="alert" className="text-sm text-red-400">{error}</p>}
      </div>
    </RebuildScreen>
  )
}
