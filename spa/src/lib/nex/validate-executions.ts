// spa/src/lib/nex/validate-executions.ts — the API-boundary check for an
// executions page (P-C spec §4.3). `listExecutions` returns whatever JSON
// the daemon sent; the list store and the table's archived query commit it
// into rendered state shared by two views, so one malformed row must never
// be able to unmount either of them. Rows without a usable identity are
// dropped; every field the views render is coerced to the shape the type
// promises. Unknown fields pass through untouched.
import type { ExecutionLeaseView, ExecutionSummary, WorkerActivity } from './types'

export interface SanitizedExecutionsPage {
  items: ExecutionSummary[]
  /** Rows dropped for lacking `id`/`state`; `1` when the page itself was not a list. */
  dropped: number
}

const isRecord = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v)
const str = (v: unknown): string => (typeof v === 'string' ? v : '')
const optStr = (v: unknown): string | undefined => (typeof v === 'string' ? v : undefined)
const num = (v: unknown): number => (typeof v === 'number' && Number.isFinite(v) ? v : 0)

function labelsOf(v: unknown): Record<string, string> {
  if (!isRecord(v)) return {}
  const out: Record<string, string> = {}
  for (const [k, val] of Object.entries(v)) if (typeof val === 'string') out[k] = val
  return out
}

function leaseOf(v: unknown): ExecutionLeaseView | undefined {
  if (!isRecord(v) || typeof v.principal_id !== 'string') return undefined
  return { principal_id: v.principal_id, expires_at: num(v.expires_at) }
}

function sanitizeRow(raw: unknown): ExecutionSummary | null {
  if (!isRecord(raw) || typeof raw.id !== 'string' || typeof raw.state !== 'string') return null
  const row: ExecutionSummary = {
    ...(raw as unknown as ExecutionSummary),
    id: raw.id,
    state: raw.state,
    provider: str(raw.provider),
    cwd: str(raw.cwd),
    brief: str(raw.brief),
    origin: optStr(raw.origin),
    labels: labelsOf(raw.labels),
    created_at: num(raw.created_at),
    updated_at: num(raw.updated_at),
    observers: num(raw.observers),
    archived: raw.archived === true,
  }
  const lease = leaseOf(raw.lease)
  if (lease) row.lease = lease
  else delete row.lease
  if (row.origin === undefined) delete row.origin
  for (const k of OPTIONAL_STRINGS) {
    const v = optStr(raw[k])
    if (v === undefined) delete row[k]
    else row[k] = v
  }
  applyRollup(row, raw)
  return row
}

const isFiniteNum = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v)
const isCount = (v: unknown): v is number => typeof v === 'number' && Number.isInteger(v) && v >= 0

/**
 * The worker_rollup fields (nexen v0.13). Absent keys stay absent — an old
 * daemon's row must not look like "0 running, no cost". A present but
 * malformed `cost_usd` is `null` (the contract's own "no number"); the
 * others are dropped.
 */
function applyRollup(row: ExecutionSummary, raw: Record<string, unknown>): void {
  if ('cost_usd' in raw) row.cost_usd = isFiniteNum(raw.cost_usd) && raw.cost_usd >= 0 ? raw.cost_usd : null
  for (const k of ['running_tasks', 'turn_count'] as const) {
    if (isCount(raw[k])) row[k] = raw[k]
    else delete row[k]
  }
  const lt = raw.last_tool
  if (isRecord(lt) && typeof lt.name === 'string' && typeof lt.tool_use_id === 'string' && isFiniteNum(lt.at)) {
    row.last_tool = { name: lt.name, tool_use_id: lt.tool_use_id, at: lt.at }
  } else delete row.last_tool
  const activity = activityOf(raw.activity)
  if (activity) row.activity = activity
  else delete row.activity
}

function activityOf(v: unknown): WorkerActivity | undefined {
  if (!isRecord(v) || typeof v.phase !== 'string' || !isCount(v.open_tools)) return undefined
  const out: WorkerActivity = { phase: v.phase, open_tools: v.open_tools }
  const t = v.tool
  if (isRecord(t) && typeof t.name === 'string' && typeof t.tool_use_id === 'string' && isFiniteNum(t.since)) {
    out.tool = { name: t.name, tool_use_id: t.tool_use_id, since: t.since }
  }
  if (isFiniteNum(v.since)) out.since = v.since
  return out
}

const OPTIONAL_STRINGS = [
  'account_id', 'requested_profile', 'effective_profile', 'reject_reason', 'terminal_reason',
  'last_turn_reason', 'session_id', 'resume_session_id', 'transcript_path', 'mount_kind', 'principal_id',
] as const satisfies readonly (keyof ExecutionSummary)[]

/**
 * The single GET (`getExecution`, R4 T4.1b): only the rollup fields are
 * coerced, exactly as on a list row — the summary is never rejected or
 * otherwise reshaped (the pane has its own handling of the rest). Returns a
 * copy; a body that is not an object comes back as is.
 */
export function sanitizeSummaryRollup(raw: unknown): ExecutionSummary {
  if (!isRecord(raw)) return raw as ExecutionSummary
  const out = { ...raw } as unknown as ExecutionSummary
  applyRollup(out, raw)
  return out
}

/** Never throws: a page that is not `{ items: [...] }` is an empty list with `dropped: 1`. */
export function sanitizeExecutionsPage(raw: unknown): SanitizedExecutionsPage {
  if (!isRecord(raw) || !Array.isArray(raw.items)) return { items: [], dropped: 1 }
  const items: ExecutionSummary[] = []
  let dropped = 0
  for (const entry of raw.items) {
    const row = sanitizeRow(entry)
    if (row) items.push(row)
    else dropped += 1
  }
  return { items, dropped }
}
