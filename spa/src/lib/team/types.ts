// spa/src/lib/team/types.ts — TypeScript mirror of the daemon's `internal/team/wire.go` (lead-team plan
// preamble "The wire contract"). JSON names are the contract; nothing here is persisted. Time fields are
// unix milliseconds. `payload` stays `unknown` (it is `json.RawMessage` on the wire and differs per kind);
// `leadPayloadOf` reads the lead shape with the daemon's normalisation, so a payload an older daemon left
// un-normalised still renders.

/** `HostEvent.type` of every approval event (`team.EventType`). */
export const APPROVAL_EVENT_TYPE = 'approval.request'

/** The two 分流 kinds (spec §6.6, U19) ride the same event; the Mac App draws no card for them (U19 (b)) and drops them at the WS boundary (approval-ws.ts). */
export type HookKind = 'hook_ask' | 'hook_permission'
export type ApprovalKind = 'lead' | 'self_relay' | HookKind

/** `answered_local`, `terminal_override` and `dismissed` close hook kinds only; `approved` on a hook kind means "answered remotely". */
export type ApprovalState = 'open' | 'approved' | 'denied' | 'timeout' | 'cancelled' | 'abandoned' | 'answered_local' | 'terminal_override' | 'dismissed'

export const HOOK_KINDS: readonly string[] = ['hook_ask', 'hook_permission'] satisfies HookKind[]
export const isHookKind = (kind: string): kind is HookKind => HOOK_KINDS.includes(kind)

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
  /**
   * The name the lead asked for ('' = none). Set only when the payload carries a string `team_name`, so
   * `undefined` means the daemon does not know team names (the dialog then shows no name field).
   */
  team_name?: string
}

/** `Approval.payload` for kind `self_relay` (spec §8.7): the usage the mod reported when it asked. */
export interface SelfRelayPayload {
  op_id: string
  used_percentage: number
  window: number
  model_id?: string
  effort?: string
}

/** What the user approved, as edited in the dialog. */
export interface Grant {
  max_members: number
  roots: string[]
  /** Optional on a decide body (absent keeps the requested name; '' clears it); present on a grant this daemon served. */
  team_name?: string
}

/** The audit label of whoever decided (spec §6.5). `addr` is set by the daemon from RemoteAddr. */
export interface Client {
  /**
   * `terminal` on the closes the terminal made (answered_local, terminal_override); `unattended` on the approvals the
   * daemon made itself while 無人值守模式 was on (U23, `team.UnattendedClient()`: no `addr`).
   */
  kind: 'app' | 'terminal' | 'unattended'
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
  /** Hook kinds only: the answer (`answers` for hook_ask; `behavior` for hook_permission). Never read by the Mac App. */
  hook?: Record<string, unknown>
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

/**
 * `HostEvent.value` (JSON text) of an `approval.request` event. A snapshot's `approvals` is `[]` when empty, never
 * null: the parser (approval-ws.ts) drops a snapshot that is not an array of valid approvals rather than read it as empty.
 */
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

const APPROVAL_KINDS: readonly string[] = ['lead', 'self_relay', 'hook_ask', 'hook_permission'] satisfies ApprovalKind[]

/**
 * A row whose kind this build does not know (a later daemon's): a record with a string `kind` outside
 * APPROVAL_KINDS. Such a row is skipped, never a reason to drop the frame it came in — a snapshot from a newer
 * daemon must still deliver the rows this build can show (PR #1799 attacker).
 */
export function isUnknownKindRow(v: unknown): boolean {
  return isRecord(v) && isString(v.kind) && !APPROVAL_KINDS.includes(v.kind)
}
const APPROVAL_STATES: readonly string[] = ['open', 'approved', 'denied', 'timeout', 'cancelled', 'abandoned', 'answered_local', 'terminal_override', 'dismissed'] satisfies ApprovalState[]

/** Absent (`omitempty`) or of the given type; `null` is neither. */
const optional = (v: unknown, ok: (x: unknown) => boolean): boolean => v === undefined || ok(v)
const isString = (v: unknown): v is string => typeof v === 'string'
const isNumber = (v: unknown): v is number => typeof v === 'number'

/** `Origin` as `wire.go` writes it: every required field present with its type (`name` / `cwd` / `tmux` may be ''). */
export function isOrigin(v: unknown): v is Origin {
  return isRecord(v)
    && isString(v.session_id)
    && isString(v.ref)
    && isString(v.name)
    && isString(v.cwd)
    && isString(v.tmux)
    && isNumber(v.pid)
    && isString(v.proc_start)
    && optional(v.title, isString)
    && optional(v.address, isString)
}

/**
 * The full `Approval` wire shape, checked at the trust boundary (the WS branch and the decide API): the dialog
 * reads these fields without guards, so a frame that fails here is dropped whole rather than rendered.
 */
export function isApproval(v: unknown): v is Approval {
  return isRecord(v)
    && isString(v.id) && v.id !== ''
    && isString(v.kind) && APPROVAL_KINDS.includes(v.kind)
    && isString(v.state) && APPROVAL_STATES.includes(v.state)
    && isString(v.host_id)
    && isOrigin(v.origin)
    && isRecord(v.payload)
    && isNumber(v.created_at)
    && isNumber(v.deadline_at)
    && isNumber(v.lease_until)
    && optional(v.decided_by, isRecord)
    && optional(v.decided_at, isNumber)
    && optional(v.grant, isRecord)
    && optional(v.hook, isRecord)
}

/** The self-relay payload, defensively: a missing or non-finite percentage reads as 0, strings as ''. */
export function selfRelayPayloadOf(a: Approval): SelfRelayPayload {
  const p = isRecord(a.payload) ? a.payload : {}
  const pct = typeof p.used_percentage === 'number' && Number.isFinite(p.used_percentage) ? p.used_percentage : 0
  const window = typeof p.window === 'number' && Number.isFinite(p.window) ? Math.trunc(p.window) : 0
  return {
    op_id: typeof p.op_id === 'string' ? p.op_id : '',
    used_percentage: pct,
    window,
    ...(typeof p.model_id === 'string' && p.model_id !== '' ? { model_id: p.model_id } : {}),
    ...(typeof p.effort === 'string' && p.effort !== '' ? { effort: p.effort } : {}),
  }
}

/** The lead payload, normalised as the daemon normalises it: `max_members` 0 → 3, cap 8; roots default `[origin.cwd]`. */
export function leadPayloadOf(a: Approval): LeadPayload {
  const p = isRecord(a.payload) ? a.payload : {}
  const reason = typeof p.reason === 'string' ? p.reason : ''
  const rawMembers = typeof p.max_members === 'number' && Number.isFinite(p.max_members) ? Math.trunc(p.max_members) : 0
  const max_members = rawMembers <= 0 ? DEFAULT_MAX_MEMBERS : Math.min(rawMembers, MAX_MAX_MEMBERS)
  const roots = Array.isArray(p.roots) ? p.roots.filter((r): r is string => typeof r === 'string' && r !== '') : []
  const lead: LeadPayload = { reason, max_members, roots: roots.length > 0 ? roots : [a.origin.cwd] }
  if (typeof p.team_name === 'string') lead.team_name = p.team_name
  return lead
}

// ---- U23: 無人值守模式 (unattended spec D-U23-5, D-U23-6; daemon `internal/team/wire_unattended.go`) ----

/** `/api/info` capabilities entry of a daemon with the switch (D-U23-5): without it the host is unsupported. */
export const UNATTENDED_CAPABILITY = 'relay.unattended.v1'
/** `HostEvent.type` of the switch's events. */
export const UNATTENDED_EVENT_TYPE = 'team.unattended'

/** The switch as the daemon stores and answers it. Times are unix ms; `since` 0 = never on, `changed_at` 0 = never written. */
export interface UnattendedState {
  on: boolean
  /** The last off→on: the "while you were away" list starts here (D-U23-6). */
  since: number
  changed_at: number
  /** Who changed it last; absent when never written. `addr` is set by the daemon. */
  changed_by?: Client
}

/** `GET` / `PUT /api/team/unattended`: the state, flattened, and one page of auto-approvals since `since`, newest first. */
export interface UnattendedView extends UnattendedState {
  approved: Approval[]
  /** More rows exist before `next_before`. */
  truncated: boolean
  /** The next page's `before` cursor (a `decided_at`); present only when truncated. */
  next_before?: number
  /** PUT only: open requests the switch-on approved. */
  swept?: number
  /** PUT only: auto-approvable requests still open after the sweep (the daemon's next tick approves them). */
  pending?: number
  /** PUT only: the write took effect but the list could not be read — `approved` is `[]` and says nothing. */
  list_failed?: boolean
}

/** `HostEvent.value` (JSON text) of a `team.unattended` event: a snapshot to each new subscriber, `changed` after every change. */
export interface UnattendedEventValue {
  op: 'snapshot' | 'changed'
  state: UnattendedState
}

const isFiniteNumber = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v)

/**
 * `Client` as the daemon writes it (`wire.go`): `kind` and `label` non-empty strings, `addr` a string when present.
 * The kind is not narrowed to the ones this build knows — a later daemon's decider is still a decider.
 */
const isClient = (v: unknown): v is Client =>
  isRecord(v)
    && isString(v.kind) && v.kind !== ''
    && isString(v.label) && v.label !== ''
    && optional(v.addr, isString)

/** The whole `UnattendedState` wire shape: a frame or an answer that fails here says nothing about the switch. */
export function isUnattendedState(v: unknown): v is UnattendedState {
  return isRecord(v)
    && typeof v.on === 'boolean'
    && isFiniteNumber(v.since)
    && isFiniteNumber(v.changed_at)
    && optional(v.changed_by, isClient)
}
