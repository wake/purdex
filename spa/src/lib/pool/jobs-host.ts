// spa/src/lib/pool/jobs-host.ts — which jobs a host's tab shows in its 工作 section (plan 7b1-4), as data. Pure; the views it builds on are in jobs-view.ts.
import type { PoolJob } from './jobs-types'
import { historyOf, lanes } from './jobs-view'

/** The ended jobs the host tab lists; the jobs tab has the rest. */
export const HOST_ENDED_SHOWN = 5

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
    queued: jobs.filter((j) => j.state === 'queued' && j.origin === alias),
    ended: historyOf(jobs, { host: alias }).slice(0, HOST_ENDED_SHOWN),
  }
}
