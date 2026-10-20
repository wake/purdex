// spa/src/lib/pool/jobs-view.ts — what the jobs tab derives from the jobs the store keeps (plan "WWD-7 design", Jobs tab). Pure: no store, no clock, no `t` of its own.
//   - the queue is shown in a *display order*, not a promise of the next claim (each worker scores for itself, wwd spec 4.6, after its labels, locks and headroom): manual `front` by `manual_seq`, then the
//     host-independent part of the score, then `back` by `manual_seq`, `hold` last;
//   - a `claimed` job (its workspace being prepared) sits in the lane of the worker that claimed it, like a running one; it is never missing from both the lane and the queue;
//   - the red line ("no host to go to") is judged against the hosts whose worker is on;
//   - the result column: a summary, else the exit code, else the reason in words.
import type { PoolZone } from './types'
import { isEnded, type PoolJob } from './jobs-types'

const MINUTE_MS = 60_000
const CLASS_WEIGHT = 1000
const WARNING_BONUS = 200
const DANGER_BONUS = 400
const SAME_SESSION_WEIGHT = 30

/** release 3, merge 2, dev 1; a class this App does not know ranks as dev (as the worker's own ranking does). */
const classRank = (c: string): number => (c === 'release' ? 3 : c === 'merge' ? 2 : 1)

/** The part of the score that does not depend on the host: `earlier` is how many queued jobs of the same submitter session were queued before this one. `zone` is the origin's. */
export function baseScore(job: PoolJob, now: number, zone: PoolZone | undefined, earlier: number): number {
  const waited = Math.max(0, Math.floor((now - job.created_at) / MINUTE_MS))
  const bonus = zone === 'danger' ? DANGER_BONUS : zone === 'warning' ? WARNING_BONUS : 0
  return CLASS_WEIGHT * classRank(job.class) + waited + bonus - SAME_SESSION_WEIGHT * earlier
}

/** Pins by `manual_seq`; each origin counts its own, so equal numbers are normal: then by `created_at`, then id (as the worker's ranking does). */
const bySeq = (a: PoolJob, b: PoolJob) => a.manual_seq - b.manual_seq || a.created_at - b.created_at || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0)

/** The queued jobs of every origin in display order. `zoneOf` is the zone of an origin (undefined when it is not known). */
export function displayOrder(jobs: readonly PoolJob[], now: number, zoneOf: (origin: string) => PoolZone | undefined): PoolJob[] {
  const queued = jobs.filter((j) => j.state === 'queued')
  // every earlier queued job of the submitter's session counts, as the worker counts them
  const times = new Map<string, number[]>()
  for (const j of queued) {
    if (j.submitter.session === '') continue
    const at = times.get(j.submitter.session)
    if (at) at.push(j.created_at)
    else times.set(j.submitter.session, [j.created_at])
  }
  const earlier = (j: PoolJob) => (j.submitter.session === '' ? 0 : (times.get(j.submitter.session) ?? []).filter((at) => at < j.created_at).length)

  const front: PoolJob[] = []
  const back: PoolJob[] = []
  const held: PoolJob[] = []
  const scored: { job: PoolJob; score: number }[] = []
  for (const j of queued) {
    if (j.manual === 'front') front.push(j)
    else if (j.manual === 'back') back.push(j)
    else if (j.manual === 'hold') held.push(j)
    else scored.push({ job: j, score: baseScore(j, now, zoneOf(j.origin), earlier(j)) })
  }
  scored.sort((a, b) => b.score - a.score || a.job.created_at - b.job.created_at || (a.job.id < b.job.id ? -1 : a.job.id > b.job.id ? 1 : 0))
  return [...front.sort(bySeq), ...scored.map((s) => s.job), ...back.sort(bySeq), ...held.sort(bySeq)]
}

/** The jobs on each worker: running ones first, then claimed ones, each by when they were claimed. A live job with no claim sits in the lane named `''` (the surface says the worker is unknown). */
export function lanes(jobs: readonly PoolJob[]): Map<string, PoolJob[]> {
  const out = new Map<string, PoolJob[]>()
  const live = jobs.filter((j) => j.state === 'running' || j.state === 'claimed')
  const rank = (j: PoolJob) => (j.state === 'running' ? 0 : 1)
  live.sort((a, b) => rank(a) - rank(b) || (a.claim?.since ?? 0) - (b.claim?.since ?? 0) || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0))
  for (const j of live) {
    const worker = j.claim?.worker ?? ''
    const lane = out.get(worker)
    if (lane) lane.push(j)
    else out.set(worker, [j])
  }
  return out
}

export interface PoolHostLabels { alias: string; labels: readonly string[]; workerEnabled: boolean }

/** Could this host take the job, setting load aside: its worker is on and it holds every label of the job. */
export const hostCanTake = (job: PoolJob, h: PoolHostLabels): boolean => h.workerEnabled && job.labels.every((l) => h.labels.includes(l))

/**
 * The red line: no host whose worker is on holds every label of the job. A host that is only busy, held back by a lock, an active owner or a 「暫停」 is a passing state and does not count here.
 * Null while no host is known at all (the pool has not been read): nothing is said then.
 */
export function noHostToGoTo(job: PoolJob, hosts: readonly PoolHostLabels[]): { labels: string[] } | null {
  if (hosts.length === 0) return null
  const fits = hosts.some((h) => hostCanTake(job, h))
  return fits ? null : { labels: [...job.labels] }
}

/** The state groups the history filters by: cancelled takes superseded with it. A state the App does not know is in no group, so only the unfiltered list shows it. */
export const STATE_GROUPS = ['failed', 'succeeded', 'cancelled', 'expired'] as const
export type StateGroup = (typeof STATE_GROUPS)[number]
const GROUP_STATES: Record<StateGroup, readonly string[]> = { failed: ['failed'], succeeded: ['succeeded'], cancelled: ['cancelled', 'superseded'], expired: ['expired'] }

/** `group` absent = every ended job; `host` is the host a job ran on, `repo` its repo name. */
export interface HistoryFilter { group?: StateGroup; host?: string; repo?: string }

/** The host a job ran on: who claimed it (else where it ended). A job that never ran (superseded or timed out in the queue) has none: only the unfiltered list shows it. */
const ranOn = (j: PoolJob): string => j.claim?.worker || j.result?.host || ''

/** The ended jobs, newest first (by when they finished, else when they were queued), under the filter. */
export function historyOf(jobs: readonly PoolJob[], filter: HistoryFilter): PoolJob[] {
  const at = (j: PoolJob) => j.result?.finished_at ?? j.created_at
  return jobs
    .filter((j) => isEnded(j.state))
    .filter((j) => (filter.group === undefined || GROUP_STATES[filter.group].includes(j.state)) && (filter.repo === undefined || j.repo.name === filter.repo) && (filter.host === undefined || ranOn(j) === filter.host))
    .sort((a, b) => at(b) - at(a) || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0))
}

const sorted = (values: Iterable<string>): string[] => [...new Set(values)].filter((v) => v !== '').sort()

/** The hosts jobs ran on, each once, in order: the choices of the host filter. */
export const historyHosts = (jobs: readonly PoolJob[]): string[] => sorted(jobs.filter((j) => isEnded(j.state)).map(ranOn))
/** The repos of the ended jobs, each once, in order: the choices of the repo filter. */
export const historyRepos = (jobs: readonly PoolJob[]): string[] => sorted(jobs.filter((j) => isEnded(j.state)).map((j) => j.repo.name))
/** The host a history row shows (`''`: it never ran). */
export const jobRanOn = ranOn

const pad = (n: number): string => String(n).padStart(2, '0')

/** When a job ended: today the time, this year the date and the time, another year with the year; 「—」 when it is not known. */
export function whenText(at: number | null, now: number): string {
  if (at === null) return '—'
  const d = new Date(at)
  const n = new Date(now)
  const time = `${pad(d.getHours())}:${pad(d.getMinutes())}`
  if (d.getFullYear() === n.getFullYear() && d.getMonth() === n.getMonth() && d.getDate() === n.getDate()) return time
  const date = `${pad(d.getMonth() + 1)}/${pad(d.getDate())}`
  return d.getFullYear() === n.getFullYear() ? `${date} ${time}` : `${d.getFullYear()}/${date} ${time}`
}

/** What the result column says, before it is turned into words. */
export type ResultWord =
  | { kind: 'superseded_by'; by: string | null }
  | { kind: 'superseded' }
  | { kind: 'summary'; passed: number; failed: number }
  | { kind: 'exit'; code: number }
  | { kind: 'reason'; reason: string }
  | { kind: 'none' }

const SUPERSEDED_BY = 'superseded_by:'
/** The reasons the daemon names today; any other is shown as it is. */
export const RESULT_REASONS = ['lost', 'oom', 'timeout', 'infra', 'queue_timeout'] as const

export function resultWord(job: PoolJob): ResultWord {
  const r = job.result
  // a newer job took its place, whatever the state: a running interruptible job ends `cancelled` with it, a non-interruptible one `succeeded` or `failed`
  if (r && (r.superseded_by || r.reason.startsWith(SUPERSEDED_BY))) return { kind: 'superseded_by', by: r.superseded_by || r.reason.slice(SUPERSEDED_BY.length) || null }
  if (job.state === 'superseded') return { kind: 'superseded' }
  if (!r) return { kind: 'none' }
  if (r.summary) return { kind: 'summary', passed: r.summary.passed, failed: r.summary.failed }
  if (r.exit !== null) return { kind: 'exit', code: r.exit }
  if (r.reason !== '') return { kind: 'reason', reason: r.reason }
  return { kind: 'none' }
}

type T = (key: string, opts?: Record<string, string | number>) => string

export function resultText(word: ResultWord, t: T): string {
  switch (word.kind) {
    case 'superseded_by': return t('pool.job.result.superseded_by')
    case 'superseded': return t('pool.job.result.superseded')
    case 'summary': return word.failed > 0 ? t('pool.job.result.failed', { n: word.failed }) : t('pool.job.result.passed', { n: word.passed })
    case 'exit': return t('pool.job.result.exit', { code: word.code })
    case 'reason': return (RESULT_REASONS as readonly string[]).includes(word.reason) ? t(`pool.job.result.reason.${word.reason}`) : word.reason
    case 'none': return ''
  }
}
