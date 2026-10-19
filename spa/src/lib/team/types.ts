// spa/src/lib/team/types.ts — TypeScript mirror of the daemon's `internal/team/wire.go` (lead-team plan
// preamble "The wire contract"). JSON names are the contract; nothing here is persisted. Time fields are
// unix milliseconds. `payload` stays `unknown` (it is `json.RawMessage` on the wire and differs per kind);
// `leadPayloadOf` reads the lead shape with the daemon's normalisation, so a payload an older daemon left
// un-normalised still renders.

/** `HostEvent.type` of every approval event (`team.EventType`). */
export const APPROVAL_EVENT_TYPE = 'approval.request'

/** The two 分流 kinds (spec §6.6, U19) ride the same event; the Mac App draws no card for them (U19 (b)) and drops them at the WS boundary (approval-ws.ts). */
export type HookKind = 'hook_ask' | 'hook_permission'
export type ApprovalKind = 'lead' | 'self_relay' | 'member_relay' | 'adopt' | HookKind

/** `answered_local`, `terminal_override` and `dismissed` close hook kinds only; `approved` on a hook kind means "answered remotely". */
export type ApprovalState = 'open' | 'approved' | 'denied' | 'timeout' | 'cancelled' | 'abandoned' | 'answered_local' | 'terminal_override' | 'dismissed'

export const HOOK_KINDS: readonly string[] = ['hook_ask', 'hook_permission'] satisfies HookKind[]
export const isHookKind = (kind: string): kind is HookKind => HOOK_KINDS.includes(kind)

/** Spec §6.1–§6.2 limits (`team.DefaultMaxMembers`, `team.MaxMaxMembers`). */
export const DEFAULT_MAX_MEMBERS = 3
export const MAX_MAX_MEMBERS = 8 // the v1 ceiling and the default of a lead request's display; the v2 ceiling is `maxMembersMax(true)`

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

/** One name a lead can take at approval (name pool v2 §5.3): `keep` is the session's own name; the others are personas. */
export type LeadNameKind = 'keep' | 'former' | 'lead' | 'other'
export interface LeadNameCandidate { code: string; name: string; kind: LeadNameKind; last_at: number }

const LEAD_NAME_KINDS: readonly string[] = ['keep', 'former', 'lead', 'other']

/** The candidate list as the daemon sends it (payload or route); `undefined` when `v` is not a list (a daemon without team.lead_name.v1). A kind this App does not know reads as `other`. */
export function leadNamesOf(v: unknown): LeadNameCandidate[] | undefined {
  if (!Array.isArray(v)) return undefined
  const out: LeadNameCandidate[] = []
  for (const e of v) {
    if (!isRecord(e) || typeof e.code !== 'string' || typeof e.name !== 'string') continue
    const kind = typeof e.kind === 'string' && LEAD_NAME_KINDS.includes(e.kind) ? (e.kind as LeadNameKind) : 'other'
    if (e.code === '' && kind !== 'keep') continue
    out.push({ code: e.code, name: e.name, kind, last_at: typeof e.last_at === 'number' && Number.isFinite(e.last_at) ? e.last_at : 0 })
  }
  return out
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
  /**
   * The short label the lead asked for ('' = none; the daemon then derives one from the name, D-L3). Set only when the
   * payload carries a string `team_label`, so `undefined` means the daemon does not know labels (no label field).
   */
  team_label?: string
  /** The names the lead can take, at request time (stale by the time the dialog opens: the dialog reads a fresh list). `undefined` = the daemon has no lead names. */
  lead_names?: LeadNameCandidate[]
}

/** `Approval.payload` for kind `self_relay` (spec §8.7): the usage the mod reported when it asked. */
export interface SelfRelayPayload {
  op_id: string
  used_percentage: number
  window: number
  model_id?: string
  effort?: string
  /** 'idle' for an idle-relay ask (idle relay spec §2); absent for the threshold ask. Kept only as a non-empty string. */
  reason?: string
  /** Minutes the session had been idle, 1–59; kept only as such an integer. */
  idle_minutes?: number
  /** True when the person is back and the request is sent again after a pause (UP-5a); absent otherwise. */
  reask?: boolean
}

/**
 * `Approval.payload` for kind `member_relay` (RQ-2 spec §4.2; daemon `MemberRelayPayload`): a lead's member relay that
 * waits for a person because the lead's member pool ran out. Only the card's text: nothing in it is trusted at approve
 * (the daemon re-checks the op, the team and the member).
 */
export interface MemberRelayPayload {
  op_id: string
  team_id: string
  lead_ref: string
  lead_title: string
  member_session_id: string
  member_ref: string
  member_title: string
  used_percentage: number
}

/**
 * `Approval.payload` for kind `adopt` (adopt spec D-U24-2; daemon `team.AdoptPayload`): the lead that asks and the
 * session it wants to take in. The target fields are what the dialog shows; the daemon re-checks every refusal at approve.
 */
export interface AdoptPayload {
  team_id: string
  lead_session_id: string
  /** The target's current ref, `_xxxxxx`. */
  target_ref: string
  target_session_id: string
  title: string
  target_name: string
  target_address: string
  target_cwd: string
  /** `<session>:@<win>.%<pane>` or ''. */
  target_tmux: string
  /** The member host's id when the target lives on another host (cross-host spec §4.3); '' = the lead's own host. */
  target_host_id: string
  /** That host's alias as the lead host knows it; '' when absent. */
  target_host_alias: string
}

/** What the user approved, as edited in the dialog. */
export interface Grant {
  max_members: number
  roots: string[]
  /** Optional on a decide body (absent keeps the requested name; '' clears it); present on a grant this daemon served. */
  team_name?: string
  /**
   * Optional on a decide body (team-label D-L4: absent keeps the requested label; '' asks for it to be derived from
   * the name); on a served grant it is the explicit label, '' when the team's label was derived.
   */
  team_label?: string
  /** Optional on a decide body: the persona code the lead takes as its name; absent keeps the session's name. */
  lead_name?: string
}

/** The audit label of whoever decided (spec §6.5). `addr` is set by the daemon from RemoteAddr. */
export interface Client {
  /**
   * `terminal` on the closes the terminal made (answered_local, terminal_override); `unattended` on the approvals the
   * daemon made itself while 無人值守模式 was on (U23, `team.UnattendedClient()`: no `addr`); `person` on the cards a
   * person's own terminal command decided (`team.person.v1`, label 「使用者（終端機指令）」).
   */
  kind: 'app' | 'terminal' | 'unattended' | 'person'
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
  /** A final self-relay card past its deadline stays open (`relay.unanswered.v1`): when it ran out, unix ms. Then `deadline_at` is
   *  the 9999-12-31 sentinel, so a card with this set shows no countdown. Absent or 0 = an ordinary card. */
  unanswered_at?: number
  /** A request the daemon cancelled at approve (an adopt whose re-check failed): the refusal's code, e.g. `adopt_target_is_lead`. */
  close_reason?: string
  /** approved only. */
  grant?: Grant
  /** Hook kinds only: the answer (`answers` for hook_ask; `behavior` for hook_permission). Never read by the Mac App. */
  hook?: Record<string, unknown>
}

/** `POST /api/team/approvals/{id}/decide`. `grant` is approve-only; absent → the payload's values. */
export interface DecideRequest {
  decision: 'approve' | 'deny'
  grant?: Grant
  /**
   * hook_ask only (the dock): `approve` + `answers` (question text → answer), or `deny` + `message` (the reply in words,
   * `team.ask_chat.v1`) — one or the other, never both.
   */
  hook?: { answers?: Record<string, string>; message?: string }
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

/** `GET /api/team/inflight` (spec §9.5): what a restart of that daemon would interrupt. `relays_active` counts the relays in progress (self and member; they resume after the restart). */
export interface InflightResponse {
  approvals_open: number
  relays_active: number
}

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

const APPROVAL_KINDS: readonly string[] = ['lead', 'self_relay', 'member_relay', 'adopt', 'hook_ask', 'hook_permission'] satisfies ApprovalKind[]

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
    && optional(v.unanswered_at, isNumber)
    && optional(v.grant, isRecord)
    && optional(v.hook, isRecord)
}

/** `/api/info` capabilities entry of a daemon that leaves a final relay card open past its deadline (`unanswered_at`, pause cause `relay_unanswered`). */
export const UNANSWERED_CAPABILITY = 'relay.unanswered.v1'

/** `/api/info` capabilities entry of a daemon that has the person decider and, under 無人值守模式, refuses lead / adopt cards itself (`decided_by.kind = "person"`, `unattended_refused`, PUT `refused`). */
export const PERSON_CAPABILITY = 'team.person.v1'

/**
 * What a card this app already holds takes from a later read of the same request (a reconnect snapshot, or the re-read at its
 * deadline): when the fresh row ran out (`isUnanswered`) and the held one does not know yet, only `unanswered_at` and `deadline_at`
 * (now the sentinel). Everything else stays, so the entry and the dialog keep their identity. Otherwise the held object itself.
 */
export function withUnanswered(held: Approval, fresh: Approval): Approval {
  if (!isUnanswered(fresh) || isUnanswered(held)) return held
  return { ...held, unanswered_at: fresh.unanswered_at, deadline_at: fresh.deadline_at }
}

/** A card the daemon left open past its deadline (`unanswered_at > 0`): its `deadline_at` is a sentinel, never to be counted down. */
export function isUnanswered(a: Approval): boolean {
  return typeof a.unanswered_at === 'number' && a.unanswered_at > 0
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
    ...(typeof p.reason === 'string' && p.reason !== '' ? { reason: p.reason } : {}),
    ...(p.reask === true ? { reask: true } : {}),
    ...(typeof p.idle_minutes === 'number' && Number.isInteger(p.idle_minutes) && p.idle_minutes >= 1 && p.idle_minutes <= 59 ? { idle_minutes: p.idle_minutes } : {}),
  }
}

/** The member-relay payload, defensively: every string '' when missing or of another type, a bad percentage 0. */
export function memberRelayPayloadOf(a: Approval): MemberRelayPayload {
  const p = isRecord(a.payload) ? a.payload : {}
  const s = (k: string): string => (typeof p[k] === 'string' ? (p[k] as string) : '')
  const pct = typeof p.used_percentage === 'number' && Number.isFinite(p.used_percentage) ? p.used_percentage : 0
  return {
    op_id: s('op_id'), team_id: s('team_id'), lead_ref: s('lead_ref'), lead_title: s('lead_title'),
    member_session_id: s('member_session_id'), member_ref: s('member_ref'), member_title: s('member_title'),
    used_percentage: pct,
  }
}

/** The adopt payload, defensively: every field a string, '' when missing or of another type. */
export function adoptPayloadOf(a: Approval): AdoptPayload {
  const p = isRecord(a.payload) ? a.payload : {}
  const s = (k: string): string => (typeof p[k] === 'string' ? (p[k] as string) : '')
  return {
    team_id: s('team_id'), lead_session_id: s('lead_session_id'), target_ref: s('target_ref'),
    target_session_id: s('target_session_id'), title: s('title'), target_name: s('target_name'),
    target_address: s('target_address'), target_cwd: s('target_cwd'), target_tmux: s('target_tmux'),
    target_host_id: s('target_host_id'), target_host_alias: s('target_host_alias'),
  }
}

/**
 * A payload string for display: cut to `max` characters (the target session writes these, so they can be any length),
 * with an ellipsis when cut. The dialog shows the trusted identifiers (ref, session id) beside the label it calls the target.
 */
export function clipForDisplay(s: string, max = 200): string {
  // Control characters and the invisible direction / zero-width marks (RLO, LRI, PDI, ...) are dropped first: a target
  // session could use them to reorder what the person reads around it (host, lead) in a heading that mixes runs.
  const chars = Array.from(s.replace(/[\p{Cc}\p{Cf}\u2028\u2029]/gu, ''))
  return chars.length <= max ? chars.join('') : `${chars.slice(0, max).join('')}…`
}

/** The label of an adopt target: its title, else its name, else its ref (what the dialog and the notification call it). */
export function adoptTargetLabel(p: AdoptPayload): string {
  return p.title !== '' ? p.title : p.target_name !== '' ? p.target_name : p.target_ref
}

/** The lead payload, normalised as the daemon normalises it: `max_members` 0 → 3, cap `ceiling` (8; a host with team.max_members.v2 passes 9); roots default `[origin.cwd]`. */
export function leadPayloadOf(a: Approval, ceiling: number = MAX_MAX_MEMBERS): LeadPayload {
  const p = isRecord(a.payload) ? a.payload : {}
  const reason = typeof p.reason === 'string' ? p.reason : ''
  const rawMembers = typeof p.max_members === 'number' && Number.isFinite(p.max_members) ? Math.trunc(p.max_members) : 0
  const max_members = rawMembers <= 0 ? DEFAULT_MAX_MEMBERS : Math.min(rawMembers, ceiling)
  const roots = Array.isArray(p.roots) ? p.roots.filter((r): r is string => typeof r === 'string' && r !== '') : []
  const lead: LeadPayload = { reason, max_members, roots: roots.length > 0 ? roots : [a.origin.cwd] }
  if (typeof p.team_name === 'string') lead.team_name = p.team_name
  if (typeof p.team_label === 'string') lead.team_label = p.team_label
  const names = leadNamesOf(p.lead_names)
  if (names !== undefined) lead.lead_names = names
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
  /** PUT only (`team.person.v1`): the lead / adopt cards the switch-on refused (they close `cancelled` / `unattended_refused`). */
  refused?: number
  /** PUT only: auto-approvable requests still open after the sweep (the daemon's next tick approves them). */
  pending?: number
  /** PUT only: the write took effect but the list could not be read — `approved` is `[]` and says nothing. */
  list_failed?: boolean
  /**
   * GET only (daemon D1): every live session of the host with its quota. Absent = an older daemon (no section);
   * `[]` = a successful read of no session. A `null` or malformed array from the daemon is `quotasFailed`, with no rows.
   */
  quotas?: SessionQuota[]
  quotasFailed?: boolean
  /** GET only (daemon D3): the open self_relay requests held for quota. Absent = none sent; `[]` = none held. */
  held?: Approval[]
}

// ---- RQ-A: per-session relay quota (spec docs/specs/2026-10-09-relay-quota-spec-plan.md; daemon `wire_quota.go`) ----

/** `/api/info` capabilities entry of a daemon that stores and serves relay quotas. */
export const RELAY_QUOTA_CAPABILITY = 'team.relay_quota.v1'

/** The daemon lets the App raise or lower a team's member cap (`PUT /api/team/max-members`). */
export const TEAM_MAX_MEMBERS_CAPABILITY = 'team.max_members.v1'

/** The daemon lets the App edit a live team's name, label and colour (`PUT /api/team/appearance`, TR-1). */
export const TEAM_EDIT_CAPABILITY = 'team.edit.v1'

/** The daemon pauses unattended sessions whose relay quota ran out and lets the App release them (`GET /api/team/paused`, UP-5b). */
export const TEAM_PAUSE_CAPABILITY = 'team.pause.v1'

/** The cap's range (the daemon's `TeamMaxMembers`): never below 1; up to 8 against a daemon with `team.max_members.v1`, up to 9 against one that also lists v2. */
export const MAX_MEMBERS_MIN = 1
export const MAX_MEMBERS_MAX_V1 = 8
export const MAX_MEMBERS_MAX_V2 = 9
export const TEAM_MAX_MEMBERS_V2_CAPABILITY = 'team.max_members.v2'
export const maxMembersMax = (v2: boolean): number => (v2 ? MAX_MEMBERS_MAX_V2 : MAX_MEMBERS_MAX_V1)

/**
 * A cap and its usage as the daemon can truthfully report them: 1-9 members allowed, 0..cap in use. A report is the daemon's own fact, so it is judged against the
 * highest ceiling any daemon has (only a v2 daemon says 9): the host's capability probe may not have answered when the first roster frame arrives.
 */
export function isCapPair(maxMembers: unknown, inUse: unknown): maxMembers is number {
  return Number.isInteger(maxMembers) && Number.isInteger(inUse)
    && (maxMembers as number) >= MAX_MEMBERS_MIN && (maxMembers as number) <= MAX_MEMBERS_MAX_V2
    && (inUse as number) >= 0 && (inUse as number) <= (maxMembers as number)
}

/** The answer of `PUT /api/team/max-members`. */
export interface MaxMembersView {
  team_id: string
  max_members: number
  in_use: number
}
/** `HostEvent.type` of a quota change. */
export const RELAY_QUOTA_EVENT_TYPE = 'team.relay_quota'

/** The two numbers of a chain: the session's own auto-relay quota and (leads) the pool for its members' relays. Integers 0-99. */
export interface RelayQuotaPair {
  self_left: number
  member_pool_left: number
}
export type RelayQuotaField = keyof RelayQuotaPair

/** One live session of a host in `UnattendedView.quotas`. `rev` is the chain root row's write counter (daemon D2). */
export interface SessionQuota extends RelayQuotaPair {
  session_id: string
  root_session_id: string
  title?: string
  address: string
  is_lead: boolean
  rev: number
}

/** The answer of `PUT /api/team/relay-quota`. `pending_lineage`: the value went to a provisional root that will be orphaned. */
export interface RelayQuotaView extends RelayQuotaPair {
  session_id: string
  root_session_id: string
  rev: number
  pending_lineage?: boolean
  updated_at: number
  updated_by?: string
}

/** `HostEvent.value` of a `team.relay_quota` event. */
export interface RelayQuotaEvent extends RelayQuotaPair {
  op: 'changed'
  root_session_id: string
  rev: number
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
