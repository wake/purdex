// spa/src/lib/nex/validate-executions.ts — the API-boundary check for an
// executions page (P-C spec §4.3). `listExecutions` returns whatever JSON
// the daemon sent; the list store and the table's archived query commit it
// into rendered state shared by two views, so one malformed row must never
// be able to unmount either of them. Rows without a usable identity are
// dropped; every field the views render is coerced to the shape the type
// promises. Unknown fields pass through untouched.
import type { ExecutionLeaseView, ExecutionSummary } from './types'

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
    archived: Boolean(raw.archived),
  }
  const lease = leaseOf(raw.lease)
  if (lease) row.lease = lease
  else delete row.lease
  if (row.origin === undefined) delete row.origin
  return row
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
