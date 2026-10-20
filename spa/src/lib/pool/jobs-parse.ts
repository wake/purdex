// spa/src/lib/pool/jobs-parse.ts — the trust boundary of the jobs wire (`/api/pool/jobs*`, internal/wwd/wire/jobs.go, api.go). A job missing what names it (id, origin, state, repo, snapshot, task, class, a time) or carrying a claim or a
// result that does not parse is dropped whole — nothing is half-read or invented; the fields that only decorate it (lists, counts, flags) fall back to empty. A page or detail with a wrong envelope is null. A state, class or position
// the App does not know stays text.
import { isRec, list, num, str, time } from './parse'
import type { JobClaim, JobDetail, JobFailure, JobResult, JobsPage, JobSubmitter, JobSummary, PoolJob } from './jobs-types'

/** A list of strings; a Go nil slice (null) or an absent one reads as empty, a list holding anything else is not a list. */
function strings(v: unknown): string[] | null {
  if (v === null || v === undefined) return []
  return Array.isArray(v) && v.every(str) ? v : null
}

const failure = (f: unknown): JobFailure | null => (isRec(f) && str(f.name) && str(f.file) && str(f.message) ? { name: f.name, file: f.file, message: f.message } : null)

function summary(v: unknown): JobSummary | null | undefined {
  if (v === null || v === undefined) return null
  if (!isRec(v) || !num(v.passed) || !num(v.failed) || !num(v.skipped)) return undefined
  const tail = strings(v.tail)
  if (!tail) return undefined
  return { passed: v.passed, failed: v.failed, skipped: v.skipped, failures: list(v.failures, failure) ?? [], tail }
}

function result(v: unknown): JobResult | null | undefined {
  if (v === null || v === undefined) return null
  if (!isRec(v)) return undefined
  const sum = summary(v.summary)
  if (sum === undefined) return undefined
  return {
    exit: num(v.exit) ? v.exit : null,
    summary: sum,
    log: str(v.log) ? v.log : '',
    host: str(v.host) ? v.host : '',
    started_at: time(v.started_at),
    finished_at: time(v.finished_at),
    cache_hit: v.cache_hit === true,
    reason: str(v.reason) ? v.reason : '',
    superseded_by: str(v.superseded_by) && v.superseded_by !== '' ? v.superseded_by : null,
  }
}

function claim(v: unknown): JobClaim | null | undefined {
  if (v === null || v === undefined) return null
  const since = isRec(v) ? time(v.since) : null
  if (!isRec(v) || !str(v.worker) || !num(v.attempt) || since === null) return undefined
  return { worker: v.worker, attempt: v.attempt, since }
}

function submitter(v: unknown): JobSubmitter {
  const r = isRec(v) ? v : {}
  return { session: str(r.session) ? r.session : '', ref: str(r.ref) ? r.ref : '', team: str(r.team) ? r.team : '', host: str(r.host) ? r.host : '' }
}

/** One job of the wire, or null when it is malformed. */
export function parseJob(v: unknown): PoolJob | null {
  if (!isRec(v)) return null
  const created = time(v.created_at)
  const repo = v.repo
  const snap = v.snapshot
  if (!str(v.id) || v.id === '' || !str(v.origin) || !str(v.state) || !str(v.task) || !str(v.class) || created === null) return null
  if (!isRec(repo) || !str(repo.name) || !isRec(snap) || !str(snap.commit)) return null
  const args = strings(v.args)
  const labels = strings(v.labels)
  const locks = strings(v.locks)
  const cl = claim(v.claim)
  const res = result(v.result)
  if (!args || !labels || !locks || cl === undefined || res === undefined) return null
  return {
    id: v.id, origin: v.origin,
    repo: { name: repo.name, root: str(repo.root) ? repo.root : '', remote: str(repo.remote) ? repo.remote : '' },
    snapshot: { commit: snap.commit, tree: str(snap.tree) ? snap.tree : '', base: str(snap.base) ? snap.base : '', rel_cwd: str(snap.rel_cwd) ? snap.rel_cwd : '' },
    task: v.task, args, class: v.class, labels, locks,
    ...(v.slot === 'heavy' || v.slot === 'light' ? { slot: v.slot } : {}),
    cacheable: v.cacheable === true,
    timeout_s: num(v.timeout_s) ? v.timeout_s : 0,
    supersede_key: str(v.supersede_key) ? v.supersede_key : '',
    interruptible: v.interruptible === true,
    queue_timeout_s: num(v.queue_timeout_s) ? v.queue_timeout_s : 0,
    adhoc: v.adhoc === true,
    local: v.local === true,
    submitter: submitter(v.submitter),
    created_at: created, state: v.state, claim: cl, result: res,
    manual: str(v.manual) ? v.manual : '',
    manual_seq: num(v.manual_seq) ? v.manual_seq : 0,
  }
}

/** `GET /api/pool/jobs`. Every job malformed is an answer outside the contract (the caller reads it as wwd_bad_response), not an empty page. */
export function parseJobsPage(v: unknown): JobsPage | null {
  if (!isRec(v) || !str(v.cursor) || !str(v.epoch) || (v.jobs !== null && !Array.isArray(v.jobs))) return null
  const raw = v.jobs ?? []
  const jobs = list(raw, parseJob) ?? []
  if (raw.length > 0 && jobs.length === 0) return null
  return { jobs, cursor: v.cursor, epoch: v.epoch }
}

/** `GET /api/pool/jobs/{id}`: the job and its stored log (null until stored). */
export function parseJobDetail(v: unknown): JobDetail | null {
  if (!isRec(v)) return null
  const job = parseJob(v.job)
  if (!job || (v.log !== null && v.log !== undefined && !str(v.log))) return null
  return { job, log: str(v.log) ? v.log : null }
}

/** The 409 body of `GET /api/pool/jobs`: the head and epoch the daemon has now. */
export function parseCursorAhead(v: unknown): { head: string; epoch: string } | null {
  return isRec(v) && v.reason === 'cursor_ahead' && str(v.head) && str(v.epoch) ? { head: v.head, epoch: v.epoch } : null
}
