// spa/src/lib/team/relay-quota-wire.ts — the relay-quota wire shapes (spec docs/specs/2026-10-09-relay-quota-spec-plan.md §3;
// plan RQ-A Task 1; daemon `internal/team/wire_quota.go`), checked at the trust boundary. A GET row, a PUT answer and a
// host event each carry the pair and the root row's `rev` (daemon D2: +1 on every write; the App keeps the newest). An
// array of rows is accepted whole or rejected whole (no silent partial list), and a value outside 0-99 is a broken
// frame, not a number to clamp.
import type { RelayQuotaEvent, RelayQuotaView, SessionQuota } from './types'

type Rec = Record<string, unknown>
const isRecord = (v: unknown): v is Rec => typeof v === 'object' && v !== null && !Array.isArray(v)
const isStr = (v: unknown): v is string => typeof v === 'string'
const isNonEmpty = (v: unknown): v is string => isStr(v) && v !== ''
const isInt = (v: unknown): v is number => typeof v === 'number' && Number.isInteger(v)
/** A quota value: an integer 0-99 (spec §3.1). */
export const isQuotaValue = (v: unknown): v is number => isInt(v) && v >= 0 && v <= 99
const isRev = (v: unknown): v is number => isInt(v) && v >= 0

function sessionQuotaOf(v: unknown): SessionQuota | null {
  if (!isRecord(v)) return null
  if (!isNonEmpty(v.session_id) || !isNonEmpty(v.root_session_id) || !isStr(v.address)) return null
  if (v.title !== undefined && !isStr(v.title)) return null
  if (typeof v.is_lead !== 'boolean') return null
  if (!isQuotaValue(v.self_left) || !isQuotaValue(v.member_pool_left) || !isRev(v.rev)) return null
  return {
    session_id: v.session_id, root_session_id: v.root_session_id, address: v.address,
    ...(v.title !== undefined ? { title: v.title } : {}),
    is_lead: v.is_lead, self_left: v.self_left, member_pool_left: v.member_pool_left, rev: v.rev,
  }
}

/** The `quotas` array of the unattended view: every row, or `null` (not an array, or any row malformed). `[]` is a successful read of no session. */
export function parseSessionQuotas(v: unknown): SessionQuota[] | null {
  if (!Array.isArray(v)) return null
  const out: SessionQuota[] = []
  for (const r of v) {
    const q = sessionQuotaOf(r)
    if (q === null) return null
    out.push(q)
  }
  return out
}

/** The answer of `PUT /api/team/relay-quota`, or null when it is not the wire shape. */
export function parseRelayQuotaView(v: unknown): RelayQuotaView | null {
  if (!isRecord(v)) return null
  if (!isNonEmpty(v.session_id) || !isNonEmpty(v.root_session_id)) return null
  if (!isQuotaValue(v.self_left) || !isQuotaValue(v.member_pool_left) || !isRev(v.rev)) return null
  if (v.pending_lineage !== undefined && typeof v.pending_lineage !== 'boolean') return null
  if (typeof v.updated_at !== 'number' || !Number.isFinite(v.updated_at)) return null
  if (v.updated_by !== undefined && !isStr(v.updated_by)) return null
  return {
    session_id: v.session_id, root_session_id: v.root_session_id, self_left: v.self_left, member_pool_left: v.member_pool_left, rev: v.rev,
    ...(v.pending_lineage !== undefined ? { pending_lineage: v.pending_lineage } : {}),
    updated_at: v.updated_at,
    ...(v.updated_by !== undefined ? { updated_by: v.updated_by } : {}),
  }
}

/** `HostEvent.value` of a `team.relay_quota` event: the event, or why it was rejected. */
export function parseRelayQuotaEvent(value: unknown): RelayQuotaEvent | string {
  let o: unknown = value
  if (typeof value === 'string') {
    try {
      o = JSON.parse(value)
    } catch {
      return 'value is not JSON'
    }
  }
  if (!isRecord(o)) return 'value is not an object'
  if (o.op !== 'changed') return `unknown op ${JSON.stringify(o.op)}`
  if (!isNonEmpty(o.root_session_id)) return 'root_session_id'
  if (!isQuotaValue(o.self_left) || !isQuotaValue(o.member_pool_left)) return 'values'
  if (!isRev(o.rev)) return 'rev'
  return { op: 'changed', root_session_id: o.root_session_id, self_left: o.self_left, member_pool_left: o.member_pool_left, rev: o.rev }
}
