// spa/src/lib/team/approval-api.ts — SPA wrappers for the daemon's approval routes (lead-team spec §6.2,
// plan preamble "Routes"): decide one request, list the open ones, read the inflight counts. Pinned to a
// host THIS device has (`pinnedHostFetch`), never the active-host fallback — a request names a specific
// daemon.
//
// Errors: every non-2xx body is `{error, detail, approval}`; the two 409s (`request_open`,
// `already_decided`) carry the Approval, so the whole body is kept on the error instead of being
// flattened. A plain-text 404 (Go's mux, no such route) means an older daemon: code `unsupported`.
// A rejected fetch (refused, reset — the daemon is restarting) is code `network`, status 0: the dialog
// queues the decision on that code alone.
import { pinnedHostFetch } from '../host-api'
import { useHostStore } from '../../stores/useHostStore'
import type { APIError, Approval, ApprovalErrorCode, DecideRequest, InflightResponse } from './types'

/** `fetchInflight`'s budget: the restart confirm's own (daemon-restart.ts `WORKER_COUNT_TIMEOUT_MS`). */
export const INFLIGHT_TIMEOUT_MS = 3_000

export class ApprovalApiError extends Error {
  readonly status: number
  readonly code: ApprovalErrorCode
  readonly detail: string
  readonly approval: Approval | null
  /** The parsed error body when it was JSON (the 409 of max-members carries `in_use` there). */
  readonly body: Record<string, unknown> | null

  constructor(status: number, code: ApprovalErrorCode, detail = '', approval: Approval | null = null, body: Record<string, unknown> | null = null) {
    super(detail !== '' ? `approval: ${code}: ${detail}` : `approval: ${code} (HTTP ${status})`)
    this.name = 'ApprovalApiError'
    this.status = status
    this.code = code
    this.detail = detail
    this.approval = approval
    this.body = body
  }
}

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

/** A non-2xx answer of any `/api/team/*` route as a typed error (shared with unattended-api.ts). */
export async function errorFromResponse(res: Response): Promise<ApprovalApiError> {
  const fallback: ApprovalErrorCode = `http_${res.status}`
  let text = ''
  try {
    text = await res.text()
  } catch {
    return new ApprovalApiError(res.status, fallback)
  }
  let parsed: unknown
  try {
    parsed = JSON.parse(text)
  } catch {
    // Not our JSON: Go's mux answers a missing route with text — an older daemon without the team module.
    return new ApprovalApiError(res.status, res.status === 404 ? 'unsupported' : fallback)
  }
  if (!isRecord(parsed)) return new ApprovalApiError(res.status, fallback)
  const body = parsed as Partial<APIError>
  const code = typeof body.error === 'string' && body.error !== '' ? body.error : fallback
  const detail = typeof body.detail === 'string' ? body.detail : ''
  const approval = isRecord(body.approval) ? (body.approval as unknown as Approval) : null
  return new ApprovalApiError(res.status, code, detail, approval, parsed)
}

const hostConfigured = (hostId: string): boolean => Object.hasOwn(useHostStore.getState().hosts, hostId)

/** One request to a configured host's team route; every failure is an `ApprovalApiError` (shared with unattended-api.ts). */
export async function send<T>(hostId: string, path: string, init: RequestInit): Promise<T> {
  // `pinnedHostFetch` rejects an unconfigured host too, but with a plain Error; the dialog switches on `code`.
  if (!hostConfigured(hostId)) throw new ApprovalApiError(0, 'host_removed')
  let res: Response
  try {
    res = await pinnedHostFetch(hostId, path, init)
  } catch (e: unknown) {
    // A host removed in the gap between the check above and the transport failing (pinnedHostFetch's own
    // "not configured" Error, or a socket error racing the removal) is non-retryable: the dialog queues on
    // `network` alone, and there is no daemon left to replay against. Same re-check as nex-api's uploadWorkerFile.
    if (!hostConfigured(hostId)) throw new ApprovalApiError(0, 'host_removed')
    throw new ApprovalApiError(0, 'network', e instanceof Error ? e.message : String(e))
  }
  if (!res.ok) throw await errorFromResponse(res)
  return (await res.json()) as T
}

/** `POST /api/team/approvals/{id}/decide` — one click (U5b). 200 → the closed Approval; 409 `already_decided` → error with `approval`. */
export function decideApproval(hostId: string, id: string, body: DecideRequest): Promise<Approval> {
  return send<Approval>(hostId, `/api/team/approvals/${encodeURIComponent(id)}/decide`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
}

/** `GET /api/team/adoptions/{approval_id}?wait=<s>` (cross-host spec §4.3): the membership a remote adopt led to. */
export interface AdoptionAnswer { approval_id: string; state: string; code?: string }

export async function fetchAdoption(hostId: string, approvalId: string, waitS: number): Promise<AdoptionAnswer> {
  const wait = Math.max(0, Math.min(30, Math.trunc(waitS)))
  const r = await send<Partial<AdoptionAnswer>>(hostId, `/api/team/adoptions/${encodeURIComponent(approvalId)}?wait=${wait}`, { method: 'GET' })
  return { approval_id: typeof r.approval_id === 'string' ? r.approval_id : approvalId, state: typeof r.state === 'string' ? r.state : '', ...(typeof r.code === 'string' && r.code !== '' ? { code: r.code } : {}) }
}

/** `GET /api/team/approvals?state=open`. `approvals` is `[]` from the daemon, but null is tolerated. */
export async function listOpenApprovals(hostId: string): Promise<Approval[]> {
  const r = await send<{ approvals?: Approval[] | null }>(hostId, '/api/team/approvals?state=open', { method: 'GET' })
  return Array.isArray(r.approvals) ? r.approvals : []
}

/** `POST /api/relay/self` (spec §8.7 (a)): the dialog's 「這個 session 不再詢問」 sets the per-session pause. */
export interface RelaySelfResponse { self_relay: 'on' | 'off' | 'paused'; host_switch: boolean; member: boolean }

export function setSelfRelayPause(hostId: string, sessionId: string, action: 'off' | 'on' | 'status'): Promise<RelaySelfResponse> {
  return send<RelaySelfResponse>(hostId, '/api/relay/self', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ session_id: sessionId, action }),
  })
}

const count = (v: unknown): number => (typeof v === 'number' && Number.isFinite(v) && v > 0 ? Math.trunc(v) : 0)

/**
 * `GET /api/team/inflight` (spec §9.5), for the daemon-restart confirm. Bounded by its own AbortController so the
 * dialog's 3 s budget holds even when the daemon accepts and never answers; an abort rejects as code `network`,
 * like any other transport failure, and the caller falls back to its store. A missing count reads as 0.
 */
export async function fetchInflight(hostId: string, timeoutMs = INFLIGHT_TIMEOUT_MS): Promise<InflightResponse> {
  const ctl = new AbortController()
  const timer = setTimeout(() => ctl.abort(), timeoutMs)
  try {
    const r = await send<Partial<InflightResponse>>(hostId, '/api/team/inflight', { method: 'GET', signal: ctl.signal })
    return { approvals_open: count(r.approvals_open), relays_active: count(r.relays_active) }
  } finally {
    clearTimeout(timer)
  }
}
