// spa/src/lib/pool/jobs-lanes.ts — what the jobs tab draws, as data (plan "WWD-7 design", Jobs tab): a lane per pool host and the queue's entries. Pure; the derived views it builds on are in jobs-view.ts.
import type { PoolJob } from './jobs-types'
import type { PoolHost, PoolZone } from './types'
import { WAIT_LONG_S } from '../watch/queue-time'
import { displayOrder, lanes, noHostToGoTo, type PoolHostLabels } from './jobs-view'

/** What a job's card says of it: `repo task args` (the full line as its title). */
export const jobLine = (j: PoolJob): string => [j.repo.name, j.task, ...j.args].filter((x) => x !== '').join(' ')

export interface LaneModel {
  /** The worker's name (the pool host's alias); `''` is the lane of live jobs with no claim to say where they are. */
  alias: string
  /** The worker is off: the lane is one line, 「不接工作」. */
  off: boolean
  /** Heavy slots drawn: the host's own, or as many as the jobs it holds (a lane never hides a job). */
  total: number
  used: number
  /** Running ones first, then claimed ones (the workspace being prepared). */
  jobs: PoolJob[]
  /** Why the host takes no more heavy work while a slot is free (the daemon's first reason code), else null. */
  reason: string | null
}

/** One lane per pool host in the pool's order, then one for each other worker that holds a job (the unknown one last). Queue jobs run on heavy slots. */
export function laneModels(hosts: readonly PoolHost[], jobs: readonly PoolJob[]): LaneModel[] {
  const held = lanes(jobs)
  const out: LaneModel[] = hosts.map((h) => {
    const mine = held.get(h.alias) ?? []
    const off = !h.worker.enabled
    const total = Math.max(h.worker.slots.heavy, mine.length)
    const free = mine.length < total
    return { alias: h.alias, off, total, used: mine.length, jobs: mine, reason: !off && free ? h.headroom_reasons?.heavy[0] ?? null : null }
  })
  const listed = new Set(hosts.map((h) => h.alias))
  const others = [...held.keys()].filter((w) => !listed.has(w)).sort((a, b) => (a === '' ? 1 : b === '' ? -1 : a < b ? -1 : a > b ? 1 : 0))
  for (const w of others) {
    const mine = held.get(w) ?? []
    out.push({ alias: w, off: false, total: mine.length, used: mine.length, jobs: mine, reason: null })
  }
  return out
}

export interface QueueEntry {
  job: PoolJob
  /** Seconds waited (whole), and whether that is long. */
  waitS: number
  long: boolean
  /** A hold: shown dimmed, last. */
  held: boolean
  /** The red line: no host whose worker is on holds every label of the job. */
  noHost: { labels: string[] } | null
}

const zoneOfHosts = (hosts: readonly PoolHost[]) => (origin: string): PoolZone | undefined => hosts.find((h) => h.alias === origin)?.facts?.derived.zone

/** The queued jobs in display order, each with what its card says. */
export function queueEntries(jobs: readonly PoolJob[], now: number, hosts: readonly PoolHost[]): QueueEntry[] {
  const labels: PoolHostLabels[] = hosts.map((h) => ({ alias: h.alias, labels: h.labels, workerEnabled: h.worker.enabled }))
  return displayOrder(jobs, now, zoneOfHosts(hosts)).map((job) => {
    const waitS = Math.max(0, Math.floor((now - job.created_at) / 1000))
    return { job, waitS, long: waitS > WAIT_LONG_S, held: job.manual === 'hold', noHost: noHostToGoTo(job, labels) }
  })
}
