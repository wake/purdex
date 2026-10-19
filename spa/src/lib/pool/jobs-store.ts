// spa/src/lib/pool/jobs-store.ts — the jobs of each origin, with the cursor and epoch they were read at (plan "WWD-7 design", Data flow: Jobs). Nothing is kept across a restart of the App; an origin is read whole the first time:
//   - the first read of an origin is `after=0&limit=1000`, page after page until a page is shorter than the limit; later reads are `after=<cursor>`; a job that comes again replaces its copy by id;
//   - an answer whose cursor is empty keeps the previous cursor; `cursor_ahead` (409) or another epoch drops that origin's jobs and reads from 0 (once per read: a second one is an error, not a loop);
//   - every job not ended is kept, and the newest 200 ended ones;
//   - the origin's own daemon is asked when the App is connected to it (only it lists fast-lane and ad-hoc jobs), else the pool daemon forwards with `?host=` (the peer view).
// This store reads and keeps; it schedules nothing (the hook that drives it owns the timers and the source choice, and acts on the error actions of errors.ts).
import { create } from 'zustand'
import { PoolReadError } from './api'
import { classify, type PoolError } from './errors'
import { fetchJob, fetchJobsPage } from './jobs-api'
import { isEnded, type PoolJob } from './jobs-types'

export const FIRST_PAGE_LIMIT = 1000
export const ENDED_KEPT = 200
/** A guard against a daemon that never answers a short page. */
const MAX_PAGES = 50

/** Which daemon to ask, and whether it forwards: `host` is set when the daemon answers for another host (the peer view). */
export interface JobSource { daemonId: string; host?: string }

interface OriginJobs {
  jobs: Record<string, PoolJob>
  /** The cursor of the last page (`''`: none yet). */
  cursor: string
  epoch: string
}

export type ReadOutcome = { ok: true } | { ok: false; error: PoolError }

interface JobsState {
  byOrigin: Record<string, OriginJobs>
  /** The last failure of each origin's read, until a read of it succeeds. */
  errors: Record<string, PoolError | undefined>
  read: (origin: string, source: JobSource) => Promise<ReadOutcome>
  /** `jobs.changed`: that one job again. An origin never read is read whole instead. */
  readJob: (origin: string, source: JobSource, id: string) => Promise<ReadOutcome>
  drop: (origin: string) => void
  reset: () => void
}

/** The origin's own daemon (when the App is connected to it) is asked for itself; else the pool daemon forwards. `ownDaemon` is keyed by pool host alias. */
export function jobSourceFor(origin: string, ownDaemon: Readonly<Record<string, string>>, forwardVia: string | null): JobSource | null {
  if (Object.hasOwn(ownDaemon, origin)) return { daemonId: ownDaemon[origin] }
  return forwardVia ? { daemonId: forwardVia, host: origin } : null
}

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
  /** Reads in flight (one per origin) and the generation a `drop` bumps: an answer of an older generation is not adopted. */
  const inflight = new Map<string, Promise<ReadOutcome>>()
  const gens = new Map<string, number>()
  const genOf = (origin: string) => gens.get(origin) ?? 0

  async function run(origin: string, source: JobSource): Promise<ReadOutcome> {
    const gen = genOf(origin)
    const baseline = get().byOrigin[origin]
    let jobs: Record<string, PoolJob> = { ...baseline?.jobs }
    let cursor = baseline?.cursor ?? ''
    let epoch = baseline?.epoch ?? ''
    let restarted = false
    const startOver = () => { jobs = {}; cursor = ''; epoch = ''; restarted = true }

    try {
      for (let pages = 0; pages < MAX_PAGES; pages++) {
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
        if (page.jobs.length < FIRST_PAGE_LIMIT || !progressed) break
      }
    } catch (e) {
      if (genOf(origin) !== gen) return { ok: true }
      const error = errorOf(e)
      set((s) => ({ errors: { ...s.errors, [origin]: error } }))
      return { ok: false, error }
    }
    if (genOf(origin) !== gen) return { ok: true }
    set((s) => ({ byOrigin: { ...s.byOrigin, [origin]: { jobs: keep(jobs), cursor, epoch } }, errors: { ...s.errors, [origin]: undefined } }))
    return { ok: true }
  }

  return {
    byOrigin: {},
    errors: {},

    read(origin, source) {
      const running = inflight.get(origin)
      if (running) return running
      const p = run(origin, source).finally(() => { if (inflight.get(origin) === p) inflight.delete(origin) })
      inflight.set(origin, p)
      return p
    },

    async readJob(origin, source, id) {
      const base = get().byOrigin[origin]
      if (!base) return get().read(origin, source)
      const gen = genOf(origin)
      try {
        const detail = await fetchJob(source.daemonId, id, source.host)
        if (genOf(origin) !== gen) return { ok: true }
        set((s) => {
          const cur = s.byOrigin[origin]
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
            const cur = s.byOrigin[origin]
            if (!cur?.jobs[id]) return s
            const { [id]: _gone, ...rest } = cur.jobs
            return { byOrigin: { ...s.byOrigin, [origin]: { ...cur, jobs: rest } } }
          })
          return { ok: true }
        }
        set((s) => ({ errors: { ...s.errors, [origin]: error } }))
        return { ok: false, error }
      }
    },

    drop(origin) {
      gens.set(origin, genOf(origin) + 1)
      inflight.delete(origin)
      set((s) => {
        const { [origin]: _d, ...rest } = s.byOrigin
        const { [origin]: _e, ...errs } = s.errors
        return { byOrigin: rest, errors: errs }
      })
    },

    reset() {
      for (const origin of new Set([...gens.keys(), ...Object.keys(get().byOrigin)])) gens.set(origin, genOf(origin) + 1)
      inflight.clear()
      set({ byOrigin: {}, errors: {} })
    },
  }
})
