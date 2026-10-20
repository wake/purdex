// spa/src/lib/pool/queue-flow.ts — what the task queue tab draws, as data (monitoring tabs spec M24, M26–M34): the queue pool (one cell per host, the jobs it sent and why they wait), the worker pool (one cell
// per worker, a row per slot class) and the summary line. Pure: no store, no clock. A host that is not ok is read from nothing but its state (M24): the jobs store may still hold its old jobs, they are not drawn.
import type { PoolJob } from './jobs-types'
import type { PoolHostState } from './host-state'
import type { PoolLiveness } from './types'
import { historyOf, hostCanTake, lanes, type PoolHostLabels } from './jobs-view'
import type { QueueEntry } from './jobs-lanes'
import { queueStripOf } from './queue-strip'
import { WAIT_LONG_S } from '../watch/queue-time'

type Slot = 'heavy' | 'light'

/** One host in the order the monitoring surfaces keep (`useMonitorHosts`): its state carries the pool row. */
export interface FlowHost { hostId: string; alias: string; state: PoolHostState }

/** Why a worker takes no more of a class of work (M31). */
export type Busy =
  | { kind: 'worker_off' }
  | { kind: 'paused'; name: string; until: number | null }
  | { kind: 'code'; code: string }
  | { kind: 'full' }
  | { kind: 'unreadable'; state: PoolLiveness }

export interface SlotRow { slot: Slot; /** Null while the host is not read (M17). */ total: number | null; jobs: PoolJob[]; busy: Busy | null }
/** `state` is null for a worker the hosts do not list (`hostId` ''): it still holds a job, so it keeps a cell; `alias` '' is a live job with no claim. */
export interface WorkerCell { hostId: string; alias: string; state: PoolHostState | null; heavy: SlotRow; light: SlotRow }

const slotOf = (j: PoolJob): Slot => (j.slot === 'light' ? 'light' : 'heavy')
const isLive = (j: PoolJob) => j.state === 'running' || j.state === 'claimed'
const workerOn = (h: FlowHost): boolean => (h.state.state === 'ok' ? h.state.facts.worker.enabled : h.state.pool.worker.enabled)
const labelsOf = (h: FlowHost): PoolHostLabels => ({ alias: h.alias, labels: h.state.pool.labels, workerEnabled: workerOn(h) })
const heldBy = (jobs: readonly PoolJob[], alias: string, slot: Slot): PoolJob[] => (lanes(jobs).get(alias) ?? []).filter((j) => slotOf(j) === slot)

const EXCEPTION_PREFIX = 'exception:'

/** Why this host takes no more `slot` work, in the order the cell says it; null when it does. */
export function busyOf(host: FlowHost, slot: Slot, jobs: readonly PoolJob[]): Busy | null {
  const s = host.state
  if (s.state !== 'ok') return { kind: 'unreadable', state: s.state }
  if (!workerOn(host)) return { kind: 'worker_off' }
  const reasons = (s.pool.headroom_reasons?.[slot] ?? []).filter((c) => c !== 'worker_off')
  const pause = reasons.find((c) => c.startsWith(EXCEPTION_PREFIX))
  if (pause !== undefined) {
    const name = pause.slice(EXCEPTION_PREFIX.length)
    return { kind: 'paused', name, until: s.facts.exceptions.find((e) => e.name === name)?.until ?? null }
  }
  if (reasons.length > 0) return { kind: 'code', code: reasons[0] }
  return heldBy(jobs, host.alias, slot).length >= s.facts.worker.slots[slot] ? { kind: 'full' } : null
}

/** A cell for each worker that is on or still holds a job (a cell never hides a job); `off` names the other workers (M27). */
export function workerCellsOf(hosts: readonly FlowHost[], jobs: readonly PoolJob[]): { cells: WorkerCell[]; off: string[] } {
  const cells: WorkerCell[] = []
  const off: string[] = []
  const held = lanes(jobs)
  for (const h of hosts) {
    if (!workerOn(h) && (held.get(h.alias) ?? []).length === 0) { off.push(h.alias); continue }
    const row = (slot: Slot): SlotRow => ({
      slot,
      total: h.state.state === 'ok' ? h.state.facts.worker.slots[slot] : null,
      jobs: heldBy(jobs, h.alias, slot),
      busy: busyOf(h, slot, jobs),
    })
    cells.push({ hostId: h.hostId, alias: h.alias, state: h.state, heavy: row('heavy'), light: row('light') })
  }
  // like the lanes of the old page: a worker nobody lists but that holds a job is drawn after the others, the unnamed one last
  const listed = new Set(hosts.map((h) => h.alias))
  const others = [...held.keys()].filter((w) => !listed.has(w)).sort((a, b) => (a === '' ? 1 : b === '' ? -1 : a < b ? -1 : a > b ? 1 : 0))
  for (const w of others) {
    const row = (slot: Slot): SlotRow => ({ slot, total: null, jobs: heldBy(jobs, w, slot), busy: null })
    cells.push({ hostId: '', alias: w, state: null, heavy: row('heavy'), light: row('light') })
  }
  return { cells, off }
}

export type Why =
  | { kind: 'held' }
  | { kind: 'no_host'; labels: string[] }
  | { kind: 'busy'; hosts: { alias: string; busy: Busy }[] }
/** `n` jobs wait for this reason; `bad` is the red line. */
export interface WhyLine { why: Why; n: number; bad: boolean }

export interface PoolCell {
  hostId: string
  alias: string
  /** False while the host is not ok: nothing is drawn but the word (M24). */
  readable: boolean
  rows: QueueEntry[]
  /** Seconds the longest waiting job has waited (a hold does not count); null when none waits. */
  longestS: number | null
  /** Jobs this host sent that are being prepared or run, on any host. */
  away: number
  whys: WhyLine[]
}

function whyOf(e: QueueEntry, hosts: readonly FlowHost[], jobs: readonly PoolJob[]): Why | null {
  if (e.held) return { kind: 'held' }
  if (e.noHost) return { kind: 'no_host', labels: e.noHost.labels }
  const slot = slotOf(e.job)
  const candidates = hosts.filter((h) => hostCanTake(e.job, labelsOf(h)))
  const busy = candidates.map((h) => ({ alias: h.alias, busy: busyOf(h, slot, jobs) }))
  if (busy.length === 0 || busy.some((b) => b.busy === null)) return null
  return { kind: 'busy', hosts: busy as { alias: string; busy: Busy }[] }
}

const whyKey = (w: Why): string => JSON.stringify(w)

/** One cell per host in the hosts' order; `entries` are the queue in display order (`queueEntries`). */
export function poolCellsOf(hosts: readonly FlowHost[], entries: readonly QueueEntry[], jobs: readonly PoolJob[]): PoolCell[] {
  return hosts.map((h): PoolCell => {
    if (h.state.state !== 'ok') return { hostId: h.hostId, alias: h.alias, readable: false, rows: [], longestS: null, away: 0, whys: [] }
    const rows = entries.filter((e) => e.job.origin === h.alias)
    const waits = rows.filter((e) => !e.held).map((e) => e.waitS)
    const lines = new Map<string, WhyLine>()
    for (const e of rows) {
      const why = whyOf(e, hosts, jobs)
      if (why === null) continue
      const key = whyKey(why)
      const line = lines.get(key)
      if (line) line.n += 1
      else lines.set(key, { why, n: 1, bad: why.kind === 'no_host' })
    }
    return {
      hostId: h.hostId, alias: h.alias, readable: true, rows,
      longestS: waits.length > 0 ? Math.max(...waits) : null,
      away: jobs.filter((j) => isLive(j) && j.origin === h.alias).length,
      whys: [...lines.values()],
    }
  })
}

export interface FlowSummary { queued: number; heavy: number; light: number; running: number; preparing: number; held: number; longestS: number | null; long: boolean }

/** The summary line (M34). The counts are the strip's own algorithm; this page keeps the jobs it has read (no `poolReadable` gate). */
export function flowSummaryOf(entries: readonly QueueEntry[], jobs: readonly PoolJob[]): FlowSummary {
  const { counts } = queueStripOf(entries, 0, true)
  const waits = entries.filter((e) => !e.held).map((e) => e.waitS)
  const longestS = waits.length > 0 ? Math.max(...waits) : null
  return {
    queued: counts.heavy + counts.light, heavy: counts.heavy, light: counts.light,
    running: jobs.filter((j) => j.state === 'running').length,
    preparing: jobs.filter((j) => j.state === 'claimed').length,
    held: entries.filter((e) => e.held).length,
    longestS, long: longestS !== null && longestS > WAIT_LONG_S,
  }
}

/** The workers a waiting job could go to: the `host_id` of every host whose worker is on and that holds all its labels (`hostCanTake`; load is not judged). */
export const candidateWorkersOf = (job: PoolJob, hosts: readonly FlowHost[]): string[] => hosts.filter((h) => hostCanTake(job, labelsOf(h))).map((h) => h.hostId)

/** Since when a job has been running (ms): when its worker said it began, else when it was claimed. A job still being prepared has none. */
export function elapsedSince(job: PoolJob): number | null {
  if (job.state !== 'running') return null
  return job.started_at ?? job.claim?.since ?? null
}

/** The last five that ended, newest first. */
export const recentEndedOf = (jobs: readonly PoolJob[]): PoolJob[] => historyOf(jobs, {}).slice(0, 5)
