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
