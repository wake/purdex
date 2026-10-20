// spa/src/lib/pool/host-counts.ts — the number at a host cell's top right (monitoring tabs spec M6, M7): the jobs this host sent that still wait (a hold is not counted) plus the jobs its worker has claimed
// (preparing or running, whichever host sent them; `claim.worker` says whose). 0 is not shown, so a host with nothing has no entry. The colour looks at what the host SENT and still waits: one that no host with
// a worker on can take is red, else one that waited long is yellow, else neutral. Pausing, full slots, the owner at work and locks are only temporary and are not "no host" (`noHostToGoTo` already says so).
import type { QueueEntry } from './jobs-lanes'
import type { PoolJob } from './jobs-types'

export type CountTone = 'neutral' | 'warn' | 'error'
export interface HostCount { n: number; tone: CountTone }

export function hostCountsOf(entries: readonly QueueEntry[], jobs: readonly PoolJob[]): Record<string, HostCount> {
  const out: Record<string, HostCount> = {}
  const bump = (alias: string, tone: CountTone = 'neutral') => {
    const cur = out[alias] ?? { n: 0, tone: 'neutral' as CountTone }
    const rank = { neutral: 0, warn: 1, error: 2 }
    out[alias] = { n: cur.n + 1, tone: rank[tone] > rank[cur.tone] ? tone : cur.tone }
  }
  for (const e of entries) {
    if (e.held) continue
    bump(e.job.origin, e.noHost ? 'error' : e.long ? 'warn' : 'neutral')
  }
  for (const j of jobs) {
    if ((j.state === 'running' || j.state === 'claimed') && j.claim) bump(j.claim.worker)
  }
  return out
}
