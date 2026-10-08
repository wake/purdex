// spa/src/lib/team/unattended-api.ts — SPA wrappers for the daemon's 無人值守模式 switch (unattended spec D-U23-1,
// D-U23-6; plan PU-2a): `GET /api/team/unattended?before=&limit=` (the state and one page of auto-approvals since the
// last switch-on) and `PUT /api/team/unattended {on, client}` (only the App calls it; D-U23-2). Same transport and
// errors as the approval routes (`send`, approval-api.ts): pinned to a configured host, a plain-text 404 — an older
// daemon without the route — is code `unsupported`.
//
// The answer is a trust boundary like the WS frame: an envelope that is not the wire shape is rejected
// (`bad_response`), never read as "off" or as an empty page; only a row of a later daemon's kind is skipped.
import { ApprovalApiError, send } from './approval-api'
import { clientDescriptor } from './client-label'
import { isApproval, isUnattendedState, isUnknownKindRow, type Approval, type UnattendedState, type UnattendedView } from './types'

export const UNATTENDED_PATH = '/api/team/unattended'

export interface UnattendedPageQuery {
  /** `next_before` of the previous page; absent = the newest. */
  before?: number
  /** Rows per page; the daemon defaults to 50 and caps at 200. */
  limit?: number
}

const isFiniteNumber = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v)
const isCount = (v: unknown): v is number => isFiniteNumber(v) && v >= 0
const isCursor = (v: unknown): v is number => isFiniteNumber(v) && v > 0

function badResponse(what: string): never {
  throw new ApprovalApiError(200, 'bad_response', `the unattended view is not the wire shape: ${what}`)
}

/**
 * A 200's body as a view, checked whole (PU-2a review): the state, `approved` an array, `truncated` a boolean,
 * `next_before` a positive number (required when truncated), `swept` / `pending` non-negative numbers and
 * `list_failed` a boolean when present. Anything else is `bad_response` — a broken answer is never an empty page.
 * A row of a kind this build does not know is skipped; a malformed row of a known kind (or of no kind) is
 * `bad_response`, so no approval goes missing from the list silently.
 */
function viewOf(raw: unknown): UnattendedView {
  if (!isUnattendedState(raw)) badResponse('state')
  const r = raw as UnattendedState & Record<string, unknown>
  if (!Array.isArray(r.approved)) badResponse('approved is not an array')
  if (typeof r.truncated !== 'boolean') badResponse('truncated is not a boolean')
  if (r.next_before !== undefined ? !isCursor(r.next_before) : r.truncated) badResponse('next_before')
  if (r.swept !== undefined && !isCount(r.swept)) badResponse('swept')
  if (r.pending !== undefined && !isCount(r.pending)) badResponse('pending')
  if (r.list_failed !== undefined && typeof r.list_failed !== 'boolean') badResponse('list_failed')
  const approved: Approval[] = []
  for (const [i, a] of r.approved.entries()) {
    if (isUnknownKindRow(a)) continue // a later daemon's kind: not ours to show, not malformed
    if (!isApproval(a)) badResponse(`approved[${i}]`)
    approved.push(a)
  }
  return {
    on: r.on,
    since: r.since,
    changed_at: r.changed_at,
    ...(r.changed_by !== undefined ? { changed_by: r.changed_by } : {}),
    approved,
    truncated: r.truncated,
    ...(r.next_before !== undefined ? { next_before: r.next_before as number } : {}),
    ...(r.swept !== undefined ? { swept: r.swept as number } : {}),
    ...(r.pending !== undefined ? { pending: r.pending as number } : {}),
    ...(r.list_failed !== undefined ? { list_failed: r.list_failed as boolean } : {}),
  }
}

/**
 * `GET /api/team/unattended`: the switch and one page of what the daemon approved since its last switch-on. `signal`
 * lets a caller that has given up (the panel's per-host timeout, or closing) cut the request instead of leaving it open.
 */
export async function getUnattended(hostId: string, q: UnattendedPageQuery = {}, signal?: AbortSignal): Promise<UnattendedView> {
  const params = new URLSearchParams()
  if (q.before !== undefined) params.set('before', String(q.before))
  if (q.limit !== undefined) params.set('limit', String(q.limit))
  const qs = params.toString()
  return viewOf(await send<unknown>(hostId, qs === '' ? UNATTENDED_PATH : `${UNATTENDED_PATH}?${qs}`, { method: 'GET', ...(signal ? { signal } : {}) }))
}

/**
 * `PUT /api/team/unattended`: turn this host's switch on or off, signed with this app's client descriptor. A 200 is
 * success even with `list_failed` (the write took effect; read only the state from it — plan decision 30).
 */
export async function putUnattended(hostId: string, on: boolean): Promise<UnattendedView> {
  const client = await clientDescriptor()
  return viewOf(await send<unknown>(hostId, UNATTENDED_PATH, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ on, client }),
  }))
}
