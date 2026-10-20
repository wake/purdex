// spa/src/lib/pool/jobs-host.ts — which jobs a host's tab shows in its 工作 section (plan 7b1-4), as data. Pure; the views it builds on are in jobs-view.ts.
import type { PoolJob } from './jobs-types'
import { historyOf, lanes } from './jobs-view'

/** The most ended jobs the monitoring tab lists; the jobs tab has the rest. */
export const HOST_ENDED_SHOWN = 5
/** The most queued jobs the monitoring tab lists before 「還有 n 件」. */
export const HOST_QUEUE_SHOWN = 3
const HOST_ENDED_MIN = 3
/** The column is about as tall as the two process tables (9 rows): the ended ones take what the running and the queued rows (and the 「還有」 row) leave, between 3 and 5 (spec M41-3). */
export function endedShownOf(runningCount: number, queuedCount: number): number {
  const more = queuedCount > HOST_QUEUE_SHOWN ? 1 : 0
  return Math.min(HOST_ENDED_SHOWN, Math.max(HOST_ENDED_MIN, 9 - runningCount - Math.min(HOST_QUEUE_SHOWN, queuedCount) - more))
}

export interface HostJobBlocks {
  /** On this host now: claimed or running by this worker, whichever host queued them (running first). */
  running: PoolJob[]
  /** Queued from this host (its own queue), wherever they will run. */
  queued: PoolJob[]
  /** The last ended ones that ran on this host, newest first. */
  ended: PoolJob[]
}

export function hostJobBlocks(jobs: readonly PoolJob[], alias: string): HostJobBlocks {
  return {
    running: lanes(jobs).get(alias) ?? [],
    queued: jobs.filter((j) => j.state === 'queued' && j.manual !== 'hold' && j.origin === alias),
    ended: historyOf(jobs, { host: alias }).slice(0, HOST_ENDED_SHOWN),
  }
}
