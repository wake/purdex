// spa/src/lib/team/types.ts — TypeScript mirror of the daemon's `internal/team/wire.go` (lead-team plan
// preamble "The wire contract"). JSON names are the contract; nothing here is persisted. Time fields are
// unix milliseconds. `payload` stays `unknown` (it is `json.RawMessage` on the wire and differs per kind);
// `leadPayloadOf` reads the lead shape with the daemon's normalisation, so a payload an older daemon left
// un-normalised still renders.

/** `HostEvent.type` of every approval event (`team.EventType`). */
export const APPROVAL_EVENT_TYPE = 'approval.request'

export type ApprovalKind = 'lead' | 'self_relay'

export type ApprovalState = 'open' | 'approved' | 'denied' | 'timeout' | 'cancelled' | 'abandoned'

/** Spec §6.1–§6.2 limits (`team.DefaultMaxMembers`, `team.MaxMaxMembers`). */
export const DEFAULT_MAX_MEMBERS = 3
export const MAX_MAX_MEMBERS = 8

/**
 * `APIError.error` as the daemon writes it: the `Err*` constants of `wire.go` plus the module's own
 * `storage_error` (`internal/module/team/handler.go`). The 409s `request_open` and `already_decided`
 * carry the Approval.
 */
export type WireErrorCode =
  | 'bad_request'
  | 'origin_unknown'
  | 'unsupported_kind'
  | 'id_conflict'
  | 'request_open'
  | 'already_lead'
  | 'member_cannot_lead'
  | 'already_decided'
  | 'not_found'
  | 'not_ready'
  | 'storage_error'

/**
 * `ApprovalApiError.code`: a wire code, or one the SPA client makes itself — `unsupported` (plain-text
 * 404: an older daemon without the team module), `network` (the fetch rejected or was aborted),
 * `host_removed` (the host is not configured on this device), `http_<status>` (a non-2xx without our
 * JSON body). A future daemon may add codes, so any string is accepted; the union is for autocomplete.
 */
export type ApprovalErrorCode = WireErrorCode | 'unsupported' | 'network' | 'host_removed' | `http_${number}` | (string & NonNullable<unknown>)

/**
 * The requesting CC session, attributed by inbox (spec §6.2). `ref` is `_xxxxxx`; `name` may be ''.
 * `title` and `address` are `omitempty` on the wire (alpha.518+); an older daemon leaves them out, and the
 * SPA then falls back to `name` / `ref` and to `<host>/<name> [<ref>]` built locally (approval-format.ts).
 */
export interface Origin {
  session_id: string
  ref: string
  name: string
  pid: number
  proc_start: string
  cwd: string
  /** `<session>:@<win>.%<pane>` or ''. */
  tmux: string
  /** The session's title (`pdx msg name`); absent when none. */
  title?: string
  /** `<alias>/<name>` for a routable name, else `<alias>/_<ref>`; absent on an older daemon. */
  address?: string
}

/** `Approval.payload` for kind `lead`. */
export interface LeadPayload {
  reason: string
  max_members: number
  roots: string[]
}

/** What the user approved, as edited in the dialog. */
export interface Grant {
  max_members: number
  roots: string[]
}

/** The audit label of whoever decided (spec §6.5). `addr` is set by the daemon from RemoteAddr. */
export interface Client {
  kind: 'app'
  label: string
  addr?: string
}

export interface Approval {
  id: string
  kind: ApprovalKind
  host_id: string
  origin: Origin
  payload: unknown
  state: ApprovalState
  created_at: number
  deadline_at: number
  lease_until: number
  /** approved / denied only. */
  decided_by?: Client
  /** Any close; `omitempty`, so absent while open. */
  decided_at?: number
  /** approved only. */
  grant?: Grant
}

/** `POST /api/team/approvals/{id}/decide`. `grant` is approve-only; absent → the payload's values. */
export interface DecideRequest {
  decision: 'approve' | 'deny'
  grant?: Grant
  client: Client
}

/** Every non-2xx body on `/api/team/*`. */
export interface APIError {
  error: string
  detail?: string
  approval?: Approval
}

/** `HostEvent.value` (JSON text) of an `approval.request` event. A snapshot's `approvals` is `[]` when empty, never null. */
export type ApprovalEventValue =
  | { op: 'opened' | 'closed'; approval: Approval }
  | { op: 'snapshot'; approvals: Approval[] }

/** `GET /api/team/inflight` (spec §9.5): what a restart of that daemon would interrupt. `relays_active` is 0 until P6. */
export interface InflightResponse {
  approvals_open: number
  relays_active: number
}

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

/** The lead payload, normalised as the daemon normalises it: `max_members` 0 → 3, cap 8; roots default `[origin.cwd]`. */
export function leadPayloadOf(a: Approval): LeadPayload {
  const p = isRecord(a.payload) ? a.payload : {}
  const reason = typeof p.reason === 'string' ? p.reason : ''
  const rawMembers = typeof p.max_members === 'number' && Number.isFinite(p.max_members) ? Math.trunc(p.max_members) : 0
  const max_members = rawMembers <= 0 ? DEFAULT_MAX_MEMBERS : Math.min(rawMembers, MAX_MAX_MEMBERS)
  const roots = Array.isArray(p.roots) ? p.roots.filter((r): r is string => typeof r === 'string' && r !== '') : []
  return { reason, max_members, roots: roots.length > 0 ? roots : [a.origin.cwd] }
}
