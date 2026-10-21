// spa/src/lib/watch/queued-sessions.ts — which agent sessions have a pool job waiting or running (watcher-area plan §8, WWD-7b1-6).
// A subagent's command carries its main session's id (spec §2.3), so a session id is all it takes.
import type { FleetWaiter } from '../fleet/types'
import type { PoolJob } from '../pool/jobs-types'
import type { PoolHost } from '../pool/types'
import { jobLine, queueEntries } from '../pool/jobs-lanes'

/** A pool job of a tab's session that is queued, claimed or running (WWD-7b1-6): the hourglass lights for it as for a gate waiter. */
export interface QueuedJobMark {
  jobId: string
  origin: string
  /** `jobLine`: repo, task and args. */
  line: string
  state: 'queued' | 'claimed' | 'running'
  /** The place in the display order across every origin (queued only; 0 otherwise). */
  place: number
  /** When it was queued (queued) or claimed (claimed, running), ms. */
  since: number
  /** The worker that has it (claimed, running); '' when the daemon did not say. */
  worker: string
}

/** What the hourglass stands for: a gate waiter (until WWD-9 removes the gate) or a pool job. */
export type QueuedItem = FleetWaiter | QueuedJobMark

export const isJobMark = (x: QueuedItem): x is QueuedJobMark => 'jobId' in x

/**
 * The live jobs of each submitter session, by lowercase session id (a job with no session, every one submitted before #3477 was deployed, lights no tab). Running first, then claimed, then queued in
 * display order. The place of a queued job is its place in the display order of ALL queued jobs (the jobs tab's queue).
 */
export function queuedJobsBySession(jobs: readonly PoolJob[], now: number, hosts: readonly PoolHost[]): Map<string, QueuedJobMark[]> {
  const out = new Map<string, QueuedJobMark[]>()
  const live = jobs.filter((j) => (j.state === 'queued' || j.state === 'claimed' || j.state === 'running') && j.submitter.session !== '')
  if (live.length === 0) return out
  const place = new Map<string, number>()
  queueEntries(jobs, now, hosts).forEach((e, i) => place.set(`${e.job.origin}\u0000${e.job.id}`, i + 1))
  const rank = { running: 0, claimed: 1, queued: 2 } as const
  const marks = live.map((j): QueuedJobMark => {
    const state = j.state as QueuedJobMark['state']
    return {
      jobId: j.id, origin: j.origin, line: jobLine(j), state,
      place: state === 'queued' ? (place.get(`${j.origin}\u0000${j.id}`) ?? 0) : 0,
      since: state === 'queued' ? j.created_at : (j.claim?.since ?? j.created_at),
      worker: j.claim?.worker ?? '',
    }
  })
  marks.sort((a, b) => rank[a.state] - rank[b.state] || a.place - b.place || (a.jobId < b.jobId ? -1 : a.jobId > b.jobId ? 1 : 0))
  const sessionOf = new Map(live.map((j) => [`${j.origin}\u0000${j.id}`, j.submitter.session.toLowerCase()]))
  for (const m of marks) { // `marks` is sorted: each list below is in that order already
    const k = sessionOf.get(`${m.origin}\u0000${m.jobId}`)!
    const list = out.get(k)
    if (list) list.push(m)
    else out.set(k, [m])
  }
  return out
}

/** What `sharedQueuedJobs` last worked out, and what it was worked out from. */
let memo: { byOrigin: unknown; zones: string; minute: number; out: Map<string, QueuedJobMark[]> } | null = null

/**
 * `queuedJobsBySession` over every origin's jobs, worked out ONCE for all the tabs that ask (every tab has a hook): the display order sorts the whole queue, so a tab each doing it would multiply that by the tab count on every jobs push.
 * Worked out again only when the jobs store's `byOrigin` is a new object, a host's zone moved (the order reads the origin's zone), or the minute turned (the score counts whole minutes waited; the hook subscribes to the minute).
 */
export function sharedQueuedJobs(byOrigin: Readonly<Record<string, { jobs: Readonly<Record<string, PoolJob>> }>>, hosts: readonly PoolHost[], minute: number): Map<string, QueuedJobMark[]> {
  const zones = hosts.map((h) => `${h.alias}:${h.facts?.derived.zone ?? ''}`).join('|')
  if (memo && memo.byOrigin === byOrigin && memo.zones === zones && memo.minute === minute) return memo.out
  const out = queuedJobsBySession(Object.values(byOrigin).flatMap((o) => Object.values(o.jobs)), minute, hosts)
  memo = { byOrigin, zones, minute, out }
  return out
}

type T = (key: string, params?: Record<string, string | number>) => string

/** 「1 分 24 秒」 / 「24 秒」. */
function elapsed(ms: number, t: T): string {
  const s = Math.max(0, Math.floor(ms / 1000))
  return s >= 60 ? t('tab.queued.job.dur.ms', { m: Math.floor(s / 60), s: s % 60 }) : t('tab.queued.job.dur.s', { s })
}

/** 「等測試結果：spa-tsc（排隊第 1 位・已等 2 分鐘）」, 「… spa-tsc 在 air26 跑了 1 分 24 秒」, 「… 2 件（a、b）」. */
export function jobsTooltip(marks: QueuedJobMark[], now: number, t: T): string {
  if (marks.length === 0) return ''
  if (marks.length > 1) return t('tab.queued.job.many', { n: marks.length, list: marks.slice(0, 3).map((m) => m.line).join('、') })
  const m = marks[0]
  const host = m.worker || m.origin
  if (m.state === 'queued') return t('tab.queued.job.queued', { job: m.line, place: m.place, m: Math.max(0, Math.floor((now - m.since) / 60_000)) })
  if (m.state === 'claimed') return t('tab.queued.job.claimed', { job: m.line, host })
  return t('tab.queued.job.running', { job: m.line, host, d: elapsed(now - m.since, t) })
}

/** The tooltip of the hourglass: the pool jobs' (above) and, until WWD-9, the gate waiters' on a line of their own. */
export function queuedTooltip(items: QueuedItem[], now: number, t: T): string {
  const jobs = items.filter(isJobMark)
  const waiters = items.filter((x): x is FleetWaiter => !isJobMark(x))
  return [jobsTooltip(jobs, now, t), waitersTooltip(waiters, now, t)].filter((x) => x !== '').join('\n')
}

/** 「排隊中：第 {place} 位・已等 {m} 分鐘・{command}（重指令｜輕指令）」 for the longest wait, plus 「・另有 n 個」. */
function waitersTooltip(waiters: FleetWaiter[], now: number, t: T): string {
  if (waiters.length === 0) return ''
  // an unknown start (null) sorts as the shortest wait
  const since = (w: FleetWaiter) => w.since ?? Number.POSITIVE_INFINITY
  const top = waiters.reduce((a, b) => (since(b) < since(a) ? b : a))
  const m = top.since === null ? 0 : Math.max(0, Math.floor((now - top.since) / 60_000))
  const head = t('tab.queued.tooltip', {
    place: top.place, m, command: top.command, kind: t(top.class === 'light' ? 'tab.queued.light' : 'tab.queued.heavy'),
  })
  return waiters.length > 1 ? head + t('tab.queued.more', { n: waiters.length - 1 }) : head
}
