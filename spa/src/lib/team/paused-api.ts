// spa/src/lib/team/paused-api.ts — the daemon's 被擋下 list and its release (unattended quota pause spec §3.1, §3.4; UP-5b):
// `GET /api/team/paused` (the sessions held back now) and `POST /api/team/paused/{session_id}/release` (a person clears one).
// Same transport and errors as the other team routes (`send`, approval-api.ts). The list is a trust boundary: a row with a
// missing or ill-typed field is dropped, never half shown; unknown causes (a later daemon's) are dropped from a row, and a row
// left with none is dropped.
import { ApprovalApiError, send } from './approval-api'
import { descriptorFor } from './unattended-api'
import { parseCauses, type PauseCause } from './roster'

export const PAUSED_PATH = '/api/team/paused'

export type PausedRole = 'solo' | 'lead' | 'member'

export interface PausedSession {
  session_id: string
  ref: string
  title: string
  role: PausedRole
  causes: PauseCause[]
  since: number
  lead_ref: string
  bounced: number
}

const isStr = (v: unknown): v is string => typeof v === 'string'
const isNum = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v)

/** One wire row as a `PausedSession`, or null when it is not one. */
export function parsePausedRow(v: unknown): PausedSession | null {
  if (typeof v !== 'object' || v === null || Array.isArray(v)) return null
  const r = v as Record<string, unknown>
  if (!isStr(r.session_id) || r.session_id === '' || !isStr(r.ref) || !isStr(r.title) || !isStr(r.lead_ref)) return null
  if (r.role !== 'solo' && r.role !== 'lead' && r.role !== 'member') return null
  if (!isNum(r.since) || !isNum(r.bounced)) return null
  const causes = parseCauses(r.causes)
  if (causes === null) return null
  return { session_id: r.session_id, ref: r.ref, title: r.title, role: r.role, causes, since: r.since, lead_ref: r.lead_ref, bounced: r.bounced }
}

/** The body of `GET /api/team/paused` as rows; an envelope that is not `{sessions: [...]}` is `bad_response`. */
export function parsePausedView(raw: unknown): PausedSession[] {
  const sessions = typeof raw === 'object' && raw !== null ? (raw as Record<string, unknown>).sessions : undefined
  if (!Array.isArray(sessions)) throw new ApprovalApiError(200, 'bad_response', 'the paused list is not the wire shape')
  return sessions.flatMap((row) => {
    const p = parsePausedRow(row)
    return p === null ? [] : [p]
  })
}

export function fetchPaused(hostId: string, signal?: AbortSignal): Promise<PausedSession[]> {
  return send<unknown>(hostId, PAUSED_PATH, { method: 'GET', ...(signal ? { signal } : {}) }).then(parsePausedView)
}

export type ReleaseResult =
  | { kind: 'released'; n: number }
  | { kind: 'not_paused' }
  | { kind: 'bad_claim'; detail: string }
  | { kind: 'error'; message: string }

/** `POST /api/team/paused/{session_id}/release`, signed with this app's client descriptor. Never throws: every outcome is a result. */
export async function postPauseRelease(hostId: string, sessionId: string): Promise<ReleaseResult> {
  try {
    const client = await descriptorFor(hostId)
    const raw = await send<unknown>(hostId, `${PAUSED_PATH}/${encodeURIComponent(sessionId)}/release`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ client }),
    })
    const n = typeof raw === 'object' && raw !== null && !Array.isArray(raw) ? (raw as Record<string, unknown>).released : undefined
    // A 2xx that is not `{released: <non-negative integer>}` is not an answer to trust: never read it as "released 0".
    if (typeof n !== 'number' || !Number.isInteger(n) || n < 0) return { kind: 'error', message: 'bad_response' }
    return { kind: 'released', n }
  } catch (e) {
    if (e instanceof ApprovalApiError) {
      if (e.status === 404 && e.code === 'not_paused') return { kind: 'not_paused' }
      if (e.status === 400) return { kind: 'bad_claim', detail: e.detail }
      return { kind: 'error', message: e.detail !== '' ? e.detail : e.code }
    }
    return { kind: 'error', message: e instanceof Error ? e.message : String(e) }
  }
}
