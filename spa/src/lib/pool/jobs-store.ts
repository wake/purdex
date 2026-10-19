// spa/src/lib/pool/jobs-store.ts — the jobs of each origin, with the cursor and epoch they were read at (plan "WWD-7 design", Data flow: Jobs). Nothing is kept across a restart of the App; an origin is read whole the first time:
//   - the first read of an origin is `after=0&limit=1000`, page after page until a page is shorter than the limit; later reads are `after=<cursor>`; a job that comes again replaces its copy by id;
//   - an answer whose cursor is empty keeps the previous cursor; `cursor_ahead` (409) or another epoch drops that origin's jobs and reads from 0 (once per read: a second one is an error, not a loop);
//   - every job not ended is kept, and the newest 200 ended ones;
//   - the origin's own daemon is asked when the App is connected to it (only it lists fast-lane and ad-hoc jobs), else the pool daemon forwards with `?host=` (the peer view). The two views differ, and a cursor is the
//     source's own, so an origin read through another source than the last one starts over from 0;
//   - the operations on one origin run one at a time, in the order asked: a single job read after a list is applied on top of it, never under its older picture;
//   - a read that used up its pages commits what it reached with the cursor it reached (a cursor runs in the order of changes, so the next read goes on from there without a gap) and answers `more`.
// This store reads and keeps; it schedules nothing (the hook that drives it owns the timers and the source choice, acts on `more` and on the error actions of errors.ts).
import { create } from 'zustand'
import { PoolReadError } from './api'
import { classify, type PoolError } from './errors'
import { fetchJob, fetchJobsPage } from './jobs-api'
import { isEnded, type PoolJob } from './jobs-types'

export const FIRST_PAGE_LIMIT = 1000
export const ENDED_KEPT = 200
/** The most pages one read takes; a daemon that never answers a short page does not hold the store. */
export const MAX_PAGES = 50

/** Which daemon to ask, and whether it forwards: `host` is set when the daemon answers for another host (the peer view). */
export interface JobSource { daemonId: string; host?: string }

interface OriginJobs {
  jobs: Record<string, PoolJob>
  /** The cursor of the last page (`''`: none yet). */
  cursor: string
  epoch: string
  /** The source these were read through: a cursor is that source's own. */
  via: string
}

/** `more`: the read used up its pages before a short one; what it reached is kept, and the next read goes on. */
export type ReadOutcome = { ok: true; more?: true } | { ok: false; error: PoolError }

interface JobsState {
  byOrigin: Record<string, OriginJobs>
  /** The last failure of each origin's read, until a read of it succeeds. */
  errors: Record<string, PoolError | undefined>
  read: (origin: string, source: JobSource) => Promise<ReadOutcome>
  /** `jobs.changed`: that one job again. An origin never read (or read through another source) is read whole instead. */
  readJob: (origin: string, source: JobSource, id: string) => Promise<ReadOutcome>
  drop: (origin: string) => void
  reset: () => void
}

/** The origin's own daemon (when the App is connected to it) is asked for itself; else the pool daemon forwards. `ownDaemon` is keyed by pool host alias. */
export function jobSourceFor(origin: string, ownDaemon: Readonly<Record<string, string>>, forwardVia: string | null): JobSource | null {
  if (Object.hasOwn(ownDaemon, origin)) return { daemonId: ownDaemon[origin] }
  return forwardVia ? { daemonId: forwardVia, host: origin } : null
}

const viaOf = (s: JobSource): string => `${s.daemonId}\u0000${s.host ?? ''}`

/** An entry the record really holds: an origin named like an Object.prototype member (`toString`) has none until it is read. */
const ownOf = <T,>(rec: Record<string, T>, key: string): T | undefined => (Object.hasOwn(rec, key) ? rec[key] : undefined)

/** Every job not ended, and the newest `ENDED_KEPT` ended ones (by when they finished, else when they were queued). */
function keep(jobs: Record<string, PoolJob>): Record<string, PoolJob> {
  const ended = Object.values(jobs).filter((j) => isEnded(j.state))
  if (ended.length <= ENDED_KEPT) return jobs
  const at = (j: PoolJob) => j.result?.finished_at ?? j.created_at
  const drop = new Set(ended.sort((a, b) => at(b) - at(a)).slice(ENDED_KEPT).map((j) => j.id))
  return Object.fromEntries(Object.entries(jobs).filter(([id]) => !drop.has(id)))
}

const errorOf = (e: unknown): PoolError => (e instanceof PoolReadError ? e.poolError : { code: 'unknown', action: 'log' })

export const useJobsStore = create<JobsState>()((set, get) => {
  /** The generation a `drop` bumps: an answer of an older generation is not adopted. */
  const gens = new Map<string, number>()
  const genOf = (origin: string) => gens.get(origin) ?? 0
  /** One chain per origin: operations run one at a time, in the order asked. */
  const tails = new Map<string, Promise<unknown>>()
  const enqueue = <T,>(origin: string, fn: () => Promise<T>): Promise<T> => {
    const p = (tails.get(origin) ?? Promise.resolve()).then(fn)
    tails.set(origin, p.catch(() => undefined))
    return p
  }
  /** The newest read asked for each origin that has not finished, with the source it uses: a second ask through the same source joins it. */
  const reads = new Map<string, { via: string; p: Promise<ReadOutcome> }>()

  async function run(origin: string, source: JobSource, gen: number): Promise<ReadOutcome> {
    const via = viaOf(source)
    const baseline = ownOf(get().byOrigin, origin)
    const same = baseline !== undefined && baseline.via === via
    let jobs: Record<string, PoolJob> = same ? { ...baseline.jobs } : {}
    let cursor = same ? baseline.cursor : ''
    let epoch = same ? baseline.epoch : ''
    let restarted = false
    let pages = 0
    let finished = false
    const startOver = () => { jobs = {}; cursor = ''; epoch = ''; restarted = true; pages = 0 }

    try {
      while (pages < MAX_PAGES && !finished) {
        pages++
        let page
        try {
          page = await fetchJobsPage(source.daemonId, { host: source.host, after: cursor === '' ? '0' : cursor, limit: FIRST_PAGE_LIMIT })
        } catch (e) {
          // the cursor was made by another database: nothing of this origin's old picture holds; once per read
          if (e instanceof PoolReadError && e.poolError.action === 'reset_origin' && !restarted) { startOver(); continue }
          throw e
        }
        if (genOf(origin) !== gen) return { ok: true }
        if (page.epoch !== '' && epoch !== '' && page.epoch !== epoch) {
          if (restarted) throw new PoolReadError('/api/pool/jobs', 200, classify(409, null))
          startOver()
          continue
        }
        if (page.epoch !== '') epoch = page.epoch
        for (const j of page.jobs) jobs[j.id] = j
        const next = page.cursor !== '' ? page.cursor : cursor
        const progressed = next !== cursor
        cursor = next
        // a short page is the end; so is a full one that moves nothing (a daemon that cannot say where it stopped)
        if (page.jobs.length < FIRST_PAGE_LIMIT || !progressed) finished = true
      }
    } catch (e) {
      if (genOf(origin) !== gen) return { ok: true }
      const error = errorOf(e)
      set((s) => ({ errors: { ...s.errors, [origin]: error } }))
      return { ok: false, error }
    }
    if (genOf(origin) !== gen) return { ok: true }
    set((s) => ({ byOrigin: { ...s.byOrigin, [origin]: { jobs: keep(jobs), cursor, epoch, via } }, errors: { ...s.errors, [origin]: undefined } }))
    return finished ? { ok: true } : { ok: true, more: true }
  }

  async function runJob(origin: string, source: JobSource, id: string, gen: number): Promise<ReadOutcome> {
    const base = ownOf(get().byOrigin, origin)
    if (!base || base.via !== viaOf(source)) return run(origin, source, gen)
    try {
      const detail = await fetchJob(source.daemonId, id, source.host)
      if (genOf(origin) !== gen) return { ok: true }
      set((s) => {
        const cur = ownOf(s.byOrigin, origin)
        if (!cur) return s
        return { byOrigin: { ...s.byOrigin, [origin]: { ...cur, jobs: keep({ ...cur.jobs, [id]: detail.job }) } } }
      })
      return { ok: true }
    } catch (e) {
      if (genOf(origin) !== gen) return { ok: true }
      const error = errorOf(e)
      if (error.code === 'not_found') {
        // the job is gone (kept 7 days): it leaves the list
        set((s) => {
          const cur = ownOf(s.byOrigin, origin)
          if (!cur?.jobs[id]) return s
          const { [id]: _gone, ...rest } = cur.jobs
          return { byOrigin: { ...s.byOrigin, [origin]: { ...cur, jobs: rest } } }
        })
        return { ok: true }
      }
      set((s) => ({ errors: { ...s.errors, [origin]: error } }))
      return { ok: false, error }
    }
  }

  return {
    byOrigin: {},
    errors: {},

    read(origin, source) {
      const via = viaOf(source)
      const running = reads.get(origin)
      if (running && running.via === via) return running.p
      const gen = genOf(origin)
      const p = enqueue(origin, () => run(origin, source, gen)).finally(() => { if (reads.get(origin)?.p === p) reads.delete(origin) })
      reads.set(origin, { via, p })
      return p
    },

    readJob(origin, source, id) {
      const gen = genOf(origin)
      return enqueue(origin, () => runJob(origin, source, id, gen))
    },

    drop(origin) {
      gens.set(origin, genOf(origin) + 1)
      reads.delete(origin)
      set((s) => {
        const { [origin]: _d, ...rest } = s.byOrigin
        const { [origin]: _e, ...errs } = s.errors
        return { byOrigin: rest, errors: errs }
      })
    },

    reset() {
      for (const origin of new Set([...gens.keys(), ...Object.keys(get().byOrigin)])) gens.set(origin, genOf(origin) + 1)
      reads.clear()
      set({ byOrigin: {}, errors: {} })
    },
  }
})
