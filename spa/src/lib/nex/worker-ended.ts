// spa/src/lib/nex/worker-ended.ts — when a worker's conversation is over (conversation entity spec §6, §7): exited, or failed to start. Shared by the ended
// screen (WorkerEndedPane), the deck's ended footer and the hook that rebuilds.
import { isLiveRow } from './live-workers'
import type { ExecutionSummary } from './types'

export type WorkerEndedKind = 'exited' | 'failed'

/** Not live → exited; live but failed / rejected → failed to start; anything else is a running pane (null). */
export function workerEndedKind(s: ExecutionSummary | null): WorkerEndedKind | null {
  if (!s) return null
  if (!isLiveRow(s)) return 'exited'
  return s.state === 'failed' || s.state === 'rejected' ? 'failed' : null
}

type T = (key: string, params?: Record<string, string | number>) => string

/** The row's words, or '' when the worker is neither waiting to start nor over. `starting`: it runs but has no session yet (the first turn reports it). */
export function lifecycleText(summary: ExecutionSummary, starting: boolean, t: T): string {
  const kind = workerEndedKind(summary)
  if (kind === 'failed') {
    const reason = summary.reject_reason || summary.terminal_reason
    return [t('worker.ended.failed_title'), ...(reason ? [t('worker.ended.reason', { reason })] : [])].join(' · ')
  }
  if (kind === 'exited') {
    const reason = summary.terminal_reason
    return [t('worker.ended.exited_title'), ...(reason ? [t('worker.ended.reason', { reason })] : [])].join(' · ')
  }
  if (summary.state === 'queued') return t('executions.activity.queued')
  return starting ? t('executions.activity.starting') : ''
}
