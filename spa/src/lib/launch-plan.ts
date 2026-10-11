// spa/src/lib/launch-plan.ts — the no-agent launch panel's daemon calls (spec 2026-10-12-no-agent-launch-panel §2.2 / §2.3).
// The daemon owns every rule (last run, project directory, the typed line, the busy check, the claim); this file only
// reads its plan and posts a choice. A missing or malformed field is read as "not available", never guessed.
import { pinnedHostFetch } from './host-api'

export type ResumeReason = 'no_record' | 'no_template' | 'empty_command'

export interface LaunchPlan {
  agentRunning: boolean
  tmuxInstance: string
  dir: { path: string; state: 'move' | 'here' } | null
  resume: { line: string } | { line: null; reason: ResumeReason }
  fresh: { line: string } | null
  busy: { command: string } | null
}

export type LaunchKind = 'resume' | 'fresh'

const RESUME_REASONS: readonly ResumeReason[] = ['no_record', 'no_template', 'empty_command']
const isRecord = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v)
const str = (v: unknown): string => (typeof v === 'string' ? v : '')

/** Lenient read of a plan body. Returns null when it is not even an object with a boolean `agent_running`. */
export function parseLaunchPlan(raw: unknown): LaunchPlan | null {
  if (!isRecord(raw) || typeof raw.agent_running !== 'boolean') return null
  const dirRaw = raw.dir
  const dir = isRecord(dirRaw) && str(dirRaw.path) !== '' && (dirRaw.state === 'move' || dirRaw.state === 'here')
    ? { path: str(dirRaw.path), state: dirRaw.state as 'move' | 'here' }
    : null
  const r = raw.resume
  let resume: LaunchPlan['resume']
  if (isRecord(r) && typeof r.line === 'string' && r.line !== '') resume = { line: r.line }
  else {
    const reason = isRecord(r) ? r.reason : undefined
    resume = { line: null, reason: RESUME_REASONS.includes(reason as ResumeReason) ? (reason as ResumeReason) : 'no_record' }
  }
  const f = raw.fresh
  const fresh = isRecord(f) && str(f.line) !== '' ? { line: str(f.line) } : null
  // busy is `null | {command: string}` (spec §2.2); a malformed one makes the whole plan unusable rather than guessing.
  const b = raw.busy
  let busy: LaunchPlan['busy'] = null
  if (b !== null && b !== undefined) {
    if (!isRecord(b) || typeof b.command !== 'string' || b.command === '') return null
    busy = { command: b.command }
  }
  return { agentRunning: raw.agent_running, tmuxInstance: str(raw.tmux_instance), dir, resume, fresh, busy }
}

const sessionPath = (code: string, tail: string) => `/api/sessions/${encodeURIComponent(code)}/${tail}`

/** The plan, or throws (any failure: the caller keeps today's view). */
export async function fetchLaunchPlan(hostId: string, code: string, signal?: AbortSignal): Promise<LaunchPlan> {
  const res = await pinnedHostFetch(hostId, sessionPath(code, 'launch-plan'), { signal })
  if (!res.ok) throw new Error(`launch-plan ${res.status}`)
  const plan = parseLaunchPlan(await res.json().catch(() => null))
  if (!plan) throw new Error('launch-plan: malformed body')
  return plan
}

/** The daemon's stable refusal codes; anything else (or no JSON body) is `other`. */
export type LaunchErrorCode = 'generation_conflict' | 'busy' | 'already_launching' | 'agent_running' | 'not_found' | 'other'

const KNOWN_CODES = ['generation_conflict', 'busy', 'already_launching', 'agent_running'] as const

export class LaunchError extends Error {
  readonly status: number
  readonly code: LaunchErrorCode
  constructor(status: number, code: LaunchErrorCode) {
    super(`launch failed (${status}${code === 'other' ? '' : ` ${code}`})`)
    this.name = 'LaunchError'
    this.status = status
    this.code = code
  }
}

/** Posts the choice; the daemon re-derives the line. Resolves on 200, throws `LaunchError` otherwise. */
export async function postLaunch(hostId: string, code: string, body: { kind: LaunchKind; cd: boolean; expectedTmuxInstance: string }): Promise<void> {
  const res = await pinnedHostFetch(hostId, sessionPath(code, 'launch'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ kind: body.kind, cd: body.cd, expected_tmux_instance: body.expectedTmuxInstance }),
  })
  if (res.ok) return
  if (res.status === 404) throw new LaunchError(404, 'not_found')
  let code2: LaunchErrorCode = 'other'
  if (res.status === 409) {
    const j: unknown = await res.json().catch(() => null)
    const c = isRecord(j) ? j.code : undefined
    if ((KNOWN_CODES as readonly unknown[]).includes(c)) code2 = c as LaunchErrorCode
  }
  throw new LaunchError(res.status, code2)
}

/** Always single-quoted; each `'` becomes `'\\''` (the daemon's fixed format). */
const shellQuote = (p: string): string => `'${p.replace(/'/g, `'\\''`)}'`

/** The line a button will type, as a preview only (the daemon composes the real one). */
export function previewLine(line: string | null, dir: LaunchPlan['dir'], cd: boolean): string {
  if (!line) return ''
  return cd && dir?.state === 'move' ? `cd ${shellQuote(dir.path)} && ${line}` : line
}
