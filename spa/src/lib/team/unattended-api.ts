// spa/src/lib/team/unattended-api.ts — SPA wrappers for the daemon's 無人值守模式 switch (unattended spec D-U23-1,
// D-U23-6; plan PU-2a): `GET /api/team/unattended?before=&limit=` (the state and one page of auto-approvals since the
// last switch-on) and `PUT /api/team/unattended {on, client}` (only the App calls it; D-U23-2). Same transport and
// errors as the approval routes (`send`, approval-api.ts): pinned to a configured host, a plain-text 404 — an older
// daemon without the route — is code `unsupported`.
//
// The answer is a trust boundary like the WS frame: a state that is not the wire shape is rejected (`bad_response`),
// never read as "off"; a row that is not an approval (or is a later daemon's kind) is skipped, the page still shown.
import { ApprovalApiError, send } from './approval-api'
import { clientDescriptor } from './client-label'
import { isApproval, isUnattendedState, type UnattendedView } from './types'

export const UNATTENDED_PATH = '/api/team/unattended'

export interface UnattendedPageQuery {
  /** `next_before` of the previous page; absent = the newest. */
  before?: number
  /** Rows per page; the daemon defaults to 50 and caps at 200. */
  limit?: number
}

/** A 200's body as a view; a state that is not the wire shape is `bad_response`. */
function viewOf(raw: unknown): UnattendedView {
  if (!isUnattendedState(raw)) throw new ApprovalApiError(200, 'bad_response', 'the unattended state is not the wire shape')
  const r = raw as Partial<UnattendedView> & Record<string, unknown>
  const approved = Array.isArray(r.approved) ? r.approved.filter(isApproval) : []
  return { ...r, approved, truncated: r.truncated === true } as UnattendedView
}

/** `GET /api/team/unattended`: the switch and one page of what the daemon approved since its last switch-on. */
export async function getUnattended(hostId: string, q: UnattendedPageQuery = {}): Promise<UnattendedView> {
  const params = new URLSearchParams()
  if (q.before !== undefined) params.set('before', String(q.before))
  if (q.limit !== undefined) params.set('limit', String(q.limit))
  const qs = params.toString()
  return viewOf(await send<unknown>(hostId, qs === '' ? UNATTENDED_PATH : `${UNATTENDED_PATH}?${qs}`, { method: 'GET' }))
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
