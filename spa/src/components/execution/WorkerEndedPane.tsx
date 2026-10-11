// spa/src/components/execution/WorkerEndedPane.tsx — what an execution pane shows
// once its worker has exited, or failed to start (conversation entity spec §6, §7):
// a dedicated screen from which the user rebuilds the conversation either in a
// terminal (take to terminal) or as a new worker (worker-rebuild). Worker is
// preselected (D12); a mode that cannot work is disabled and never left selected.
import { SmileySad } from '@phosphor-icons/react'
import { RebuildScreen } from '../RebuildScreen'
import { RebuildModeChoice } from '../RebuildModeChoice'
import { useI18nStore } from '../../stores/useI18nStore'
import { closeTab } from '../../lib/tab-lifecycle'
import { useWorkerEnded } from '../../hooks/useWorkerEnded'
import type { ExecutionSummary } from '../../lib/nex/types'

// The classification lives in lib (the deck's ended footer and the hook share it); the old import path stays.
// eslint-disable-next-line react-refresh/only-export-components
export { workerEndedKind, type WorkerEndedKind } from '../../lib/nex/worker-ended'

interface Props {
  hostId: string
  executionId: string
  summary: ExecutionSummary
  tabId: string
  paneId: string
}

export function WorkerEndedPane({ hostId, executionId, summary, tabId, paneId }: Props) {
  const t = useI18nStore((s) => s.t)
  const { kind, reason, mode, setChoice, terminalAvailable, workerAvailable, nothingAvailable, busy, exiting, error, rebuild, exit } = useWorkerEnded({ hostId, executionId, summary, tabId, paneId })

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
        {error && <p data-testid="worker-rebuild-error" role="alert" className="text-sm text-red-400">{error.message}</p>}
      </div>
    </RebuildScreen>
  )
}
