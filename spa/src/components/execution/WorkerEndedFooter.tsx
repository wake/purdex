// spa/src/components/execution/WorkerEndedFooter.tsx — what the deck's footer is once its worker is over (U4-4a; spec §3.5): where the input was, the same
// choice WorkerEndedPane offers — rebuild the conversation in a terminal or as a new worker, exit a worker that failed to start, close the tab. The
// logic is `useWorkerEnded`, the one the full ended screen uses; this is the compact layout beside the stream.
import { useI18nStore } from '../../stores/useI18nStore'
import { closeTab } from '../../lib/tab-lifecycle'
import { useWorkerEnded, type WorkerEndedProps } from '../../hooks/useWorkerEnded'
import { RebuildModeChoice } from '../RebuildModeChoice'

const BTN = 'cursor-pointer rounded border border-border-subtle px-3 py-1 text-text-primary hover:bg-surface-hover disabled:cursor-not-allowed disabled:opacity-40'

export function WorkerEndedFooter(props: WorkerEndedProps) {
  const t = useI18nStore((s) => s.t)
  const { kind, mode, setChoice, terminalAvailable, workerAvailable, nothingAvailable, busy, exiting, error, rebuild, exit } = useWorkerEnded(props)
  return (
    <div data-testid="worker-ended-footer" className="flex flex-col gap-2 border-t border-border-subtle px-3 py-2 deck-t-down text-text-secondary">
      <span>{nothingAvailable ? t('worker.rebuild.worker_unavailable') : kind === 'exited' ? t('worker.ended.exited_desc') : ''}</span>
      <RebuildModeChoice value={mode} onChange={setChoice} terminalAvailable={terminalAvailable} workerAvailable={workerAvailable}
        workerUnavailableHint={t('worker.rebuild.worker_unavailable')} disabled={busy || exiting} />
      <div className="flex flex-wrap items-center gap-2">
        <button type="button" data-testid="worker-rebuild" disabled={busy || exiting || nothingAvailable} onClick={() => { void rebuild() }} className={BTN}>{t('worker.rebuild.button')}</button>
        {kind === 'failed' && (
          <button type="button" data-testid="worker-ended-exit" disabled={exiting || busy} onClick={() => { void exit() }} className={BTN}>{t('worker.exit.button')}</button>
        )}
        <button type="button" onClick={() => closeTab(props.tabId)} className={BTN}>{t('worker.ended.close_tab')}</button>
      </div>
      {error && <p data-testid="worker-rebuild-error" role="alert" className="text-status-error">{error.message}</p>}
    </div>
  )
}
