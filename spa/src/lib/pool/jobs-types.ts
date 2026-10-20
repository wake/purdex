// spa/src/lib/pool/jobs-types.ts — the App's view of wwd's jobs (`/api/pool/jobs*`, wwd spec §4, internal/wwd/wire/jobs.go). Times are ms since epoch. A state, class or position the App does not know stays text:
// the daemon may add kinds, and an unknown one is shown as is, never an error.
export const ENDED_STATES = ['succeeded', 'failed', 'cancelled', 'superseded', 'expired'] as const
export const LIVE_STATES = ['queued', 'claimed', 'running'] as const
export type JobState = (typeof ENDED_STATES)[number] | (typeof LIVE_STATES)[number] | (string & {})
export type JobClass = 'release' | 'merge' | 'dev' | (string & {})
/** A queued job's manual place (`''`: the score decides). */
export type JobPosition = '' | 'front' | 'back' | 'hold' | (string & {})

export interface JobRepo { name: string; root: string; remote: string }
export interface JobSnapshot { commit: string; tree: string; base: string; rel_cwd: string }
/** `session` is empty for every job submitted before #3477 was deployed. */
export interface JobSubmitter { session: string; ref: string; team: string; host: string }
export interface JobClaim { worker: string; attempt: number; since: number }
export interface JobFailure { name: string; file: string; message: string }
export interface JobSummary { passed: number; failed: number; skipped: number; failures: JobFailure[]; tail: string[] }
export interface JobResult {
  exit: number | null
  summary: JobSummary | null
  log: string
  host: string
  started_at: number | null
  finished_at: number | null
  cache_hit: boolean
  /** Free text: `lost`, `oom`, `timeout`, `infra`, `queue_timeout`, `superseded_by:<id>` … */
  reason: string
  superseded_by: string | null
}

export interface PoolJob {
  id: string
  /** The host that queued it: whose queue it is in. */
  origin: string
  repo: JobRepo
  snapshot: JobSnapshot
  task: string
  args: string[]
  class: JobClass
  /** Which lane of the host it runs in; absent when the wire does not say. */
  slot?: 'heavy' | 'light'
  /** When the worker began running it (ms); absent when the wire does not say. */
  started_at?: number
  labels: string[]
  locks: string[]
  cacheable: boolean
  timeout_s: number
  supersede_key: string
  interruptible: boolean
  queue_timeout_s: number
  adhoc: boolean
  local: boolean
  submitter: JobSubmitter
  created_at: number
  state: JobState
  claim: JobClaim | null
  result: JobResult | null
  manual: JobPosition
  manual_seq: number
}

/** One page of `GET /api/pool/jobs`: jobs oldest change first, the cursor to read on from (`''`: none), the database epoch. */
export interface JobsPage { jobs: PoolJob[]; cursor: string; epoch: string }
/** `GET /api/pool/jobs/{id}`: the job and its stored log (null until stored, and when over 1 MiB). */
export interface JobDetail { job: PoolJob; log: string | null }

export const isEnded = (state: JobState): boolean => (ENDED_STATES as readonly string[]).includes(state)
