// spa/src/lib/nex/types.ts — wire shapes of the Nexen contract as mounted at
// /api/nex (nexen/docs/contract/capability-matrix.md; api/handlers.go
// executionView / eventView, api/interact.go *Wire). Only the fields the SPA
// reads are typed; index signatures keep unknown additions harmless.

export type ExecutionStateName = 'queued' | 'running' | 'idle' | 'rejected' | 'failed' | 'terminated'

export interface ExecutionLeaseView {
  principal_id: string
  expires_at: number
}

export interface ExecutionSummary {
  id: string
  state: ExecutionStateName | string
  provider: string
  principal_id: string
  cwd: string
  mount_kind: string
  account_id?: string
  brief: string
  origin?: string
  labels: Record<string, string>
  requested_profile?: string
  effective_profile?: string
  reject_reason?: string
  terminal_reason?: string
  last_turn_reason?: string
  session_id?: string
  resume_session_id?: string
  transcript_path?: string
  pid?: number
  created_at: number
  updated_at: number
  duration_ms: number | null
  event_count: number
  observers: number
  archived: boolean
  // On a worker_rollup daemon (nexen v0.13+) list rows always carry it (0 is
  // an answer); the single GET keeps omitempty. Older daemons: single GET only.
  turn_count?: number
  live_turn_id?: string
  lease?: ExecutionLeaseView
  // Rollup fields (capabilities.worker_rollup; absent on an older daemon).
  /** The conversation's cumulative cost (resume chain, may predate the execution); null = none reported. */
  cost_usd?: number | null
  last_tool?: { name: string; tool_use_id: string; at: number }
  running_tasks?: number
  activity?: WorkerActivity
  /**
   * A human-readable name for the provider session, read from the transcript
   * (nexen contract §1.10). Key absent = no title yet — NOT null, NOT `{}`,
   * NOT `text: ""`. Present ⇒ `text` is always non-empty. `source` is the
   * closed vocabulary in `capabilities.session_title.sources`, in priority
   * order (`custom` highest); typed loosely here since an older/newer daemon
   * could in principle send a value outside today's three.
   */
  session_title?: { text: string; source: 'custom' | 'agent_name' | 'ai' | string }
}

/** `activity` on an execution summary. `phase` is an open set — read it through `normalizePhase`. */
export interface WorkerActivity {
  phase: string
  tool?: { name: string; tool_use_id: string; since: number }
  open_tools: number
  since?: number
}

export type TaskKind = 'shell' | 'subagent' | 'other'
export type TaskStatus = 'running' | 'completed' | 'failed' | 'killed' | 'lost'

export interface TaskUsage {
  total_tokens: number
  tool_uses: number
  duration_ms: number
}

/**
 * One background task (Bash `run_in_background` / subagent) — the merged
 * `task_start` ∪ `task_end` shape, which is also a `/tasks` item. End fields
 * are null (or absent, for the optional ones) while running. `cost_usd` is
 * deliberately not carried: it is always null (`worker_rollup.subagent_cost`).
 */
export interface WorkerTask {
  task_id: string
  turn_id: string
  kind: TaskKind
  /** Provider original (`local_bash`, `local_agent`, …). */
  task_type: string
  tool_use_id: string | null
  parent_tool_use_id: string | null
  description: string
  command?: string
  subagent_type?: string
  backgrounded: boolean
  status: TaskStatus
  provider_status: string | null
  closed_by: string | null
  summary?: string
  usage?: TaskUsage
  /** Daemon observation time (ms); null only for a row known from its task_end alone. */
  started_at: number | null
  ended_at: number | null
  /**
   * Client-only: the durable seq of this row's task_start, or the snapshot
   * cursor for a row first seen in a `/tasks` snapshot. Decides whether a
   * later snapshot that omits a running row may drop it.
   */
  startSeq: number
}

export interface WorkerTasksSnapshot {
  items: WorkerTask[]
  /** The execution's latest seq, read BEFORE the rows (nexen api/tasks.go). */
  cursor: number
}

/** `capabilities.worker_rollup` — presence is the feature detect; never compare versions. */
export interface WorkerRollupCapability {
  task_kinds: string[]
  task_statuses: string[]
  activity_phases: string[]
  cost_basis?: string
  subagent_cost: boolean
}

/** `capabilities.transcript_prelude` (worker prelude spec §4.5). Presence is the only feature detect. */
export interface TranscriptPreludeCapability {
  route: { method: string; path: string }
  page_max_items: number
  page_max_bytes: number
  max_block_bytes: number
  /** Nexen v0.17: prelude items carry an integer `offset`. Check by key presence. */
  item_offset?: boolean
}

export interface NexEvent {
  seq: number
  execution_id: string
  kind: string
  payload: Record<string, unknown>
  created_at: number
}

export interface ExecutionsPage {
  items: ExecutionSummary[]
  /** Opaque; "" means this was the last page. */
  next_cursor: string
}

export interface EventsPage {
  items: NexEvent[]
  /** Seq to pass back as `after`; 0 means this was the last page. */
  next_cursor: number
}

/**
 * `capabilities.send.attachments.image` (nexen contract §0/§1.9). The whole
 * object being absent = this daemon does not accept images; presence plus
 * `providers` including the execution's `provider` is the ONLY feature
 * detect (never a version compare). Numbers are the daemon's actual caps —
 * a client must never hard-code them.
 */
export interface ImageAttachmentCaps {
  /** Accepted `media_type`s; anything else → 400 `attachment_type_unsupported`. */
  media_types: string[]
  /** Per-image **decoded** byte cap (not the base64 length); over → 400 `attachment_too_large`. */
  max_bytes: number
  /** Max images in one message; over → 400 `too_many_attachments`. */
  max_count: number
  /** Sum of all images' **decoded** bytes in one message; over → 400 `attachments_too_large`. */
  max_total_bytes: number
  /** Providers this host currently has a runner for AND that support image input. */
  providers: string[]
  /** Route to fetch the original image back; `path` is origin-relative and already carries `PublicPrefix`. */
  fetch: { method: string; path: string }
}

export interface NexCapabilities {
  phase: string
  host_id: string
  verbs: string[]
  providers: string[]
  events: string[]
  provider_events: string[]
  transient_events: string[]
  sandbox_profiles: string[]
  sandbox_default_profile: string
  sandbox_max_profile: string
  roots: Array<{ path: string; kind: 'dev' | 'service' | string }>
  lease: {
    ttl_seconds: number
    scope: string
    renew: { method: string; path: string }
    release: { method: string; path: string }
  }
  send: {
    delivery: string[]
    max_text_bytes: number
    /**
     * The whole request body cap (bytes) shared by `send` and `delegate`
     * (nexen contract §0/§1.9, 32 MiB); over → 413 `request_too_large`.
     * Absent on a daemon older than 2026-09-28.
     */
    max_request_bytes?: number
    /** Absent object, or `image` absent inside it, ⇒ this daemon does not accept images (fail-closed). */
    attachments?: { image?: ImageAttachmentCaps }
  }
  brief?: { max_bytes: number }
  origin?: { max_bytes: number }
  labels?: {
    max_count: number
    max_key_bytes: number
    max_value_bytes: number
    max_total_bytes: number
    reserved_prefix: string
  }
  delegate?: {
    resume_session_id?: boolean
    /** This build accepts `attachments` on delegate's first turn; absent/false on a daemon that does not. */
    attachments?: boolean
    /** Nexen v0.17: delegate accepts `start_idle` (worker starts idle with zero turns). Check by key presence. */
    start_idle?: boolean
  }
  /** Nexen v0.17: the execution list can be filtered by session. Check by key presence. */
  list?: { session_filter?: boolean }
  /**
   * Presence = this build may put `session_title` on execution summaries and
   * emits `execution.title_changed` (nexen contract §0/§1.10). Absence =
   * older daemon; consumer falls back on its own. Presence does NOT mean
   * every execution has a title (§2 #62) — callers still need a fallback.
   */
  session_title?: { sources: string[]; max_bytes: number }
  /**
   * Presence = the daemon emits the N2 `tool_use` / `tool_result` events
   * (nexen contract §0). The exec pane does NOT branch on it (P-B3 spec
   * §4.1: N2 is an overlay on the raw frames, never a switch). The numbers
   * are the daemon's actual caps for `output.text` / `diff.hunks`; a client
   * that shows "showing X of Y" must read them from here and never hard-code
   * them (P-B3 spec §4.5).
   */
  tool_events?: { output_max_bytes: number; diff_max_lines: number }
  /**
   * Presence = the daemon emits `task_start` / `task_end`, serves
   * `GET /v1/executions/{id}/tasks` and puts the rollup fields on execution
   * summaries (nexen v0.13, contract §0 `worker_rollup`).
   */
  worker_rollup?: WorkerRollupCapability
  /** Presence = `GET /v1/executions/{id}/prelude` exists (worker prelude spec §4). */
  transcript_prelude?: TranscriptPreludeCapability
  [key: string]: unknown
}

export interface DelegateRequest {
  brief: string
  cwd: string
  profile?: string
  labels?: Record<string, string>
  origin?: string
  resume_session_id?: string
}

export interface DelegateResult {
  id: string
  state: ExecutionStateName | string
  reject_reason?: string
  effective_profile?: string
}

export interface NexQuota {
  five_hour_pct: number
  seven_day_pct: number
  resets_at: number
  source: 'usage_api' | 'provider_event' | string
}

export interface NexHostInfo {
  active_account: string
  /**
   * Which account the reading is attributed to — the id of the token that
   * would be used, not a claim about who the user is. Omitted by a daemon
   * with no host login, and by every daemon older than nexen v0.11.0.
   */
  account_id?: string
  /**
   * Which backend the host's Claude Code login was read from. The CLI writes
   * to whichever of the two is writable and deletes the other, so this can
   * flip without anyone touching the config.
   */
  credential_source?: 'file' | 'keychain' | string
  /**
   * Non-empty exactly when the pick was ambiguous — most importantly when
   * the two backends hold DIFFERENT accounts. Saying so is the whole point
   * of nexen resolving the credential instead of letting the CLI pick one
   * silently, so the card renders it loudly rather than as another row.
   */
  credential_warning?: string
  /** null whenever the daemon cannot say whose quota it would be. */
  quota: NexQuota | null
}

export interface AttachObserveResponse {
  mode: 'observe'
  /** Origin-relative absolute path, already prefixed with /api/nex. */
  stream_url: string
  /** The execution's latest durable seq at attach time. */
  cursor: number
  state: string
}

export interface AttachControlResponse {
  mode: 'control'
  lease_id: string
  expires_at: number
}

export interface SendResponse {
  turn_id: string
  delivery: 'delivered' | 'queued'
}

export interface InterruptResponse {
  turn_id: string
  state: string
  reason?: string
}

/**
 * Every non-2xx from /api/nex. `code` is the contract's structured error
 * code (capability-matrix "錯誤碼"), or `http_<status>` when the body is not
 * the {error, code} shape (a proxy page, an older daemon, the pdx 503
 * nex_unavailable fallback still parses because it uses the same shape).
 */
export class NexApiError extends Error {
  readonly status: number
  readonly code: string
  readonly turnId?: string
  /** 0-based index of the offending image, on Nexen's per-image attachment errors (contract §1.9). */
  readonly attachmentIndex?: number

  constructor(status: number, code: string, message: string, turnId?: string, attachmentIndex?: number) {
    super(message)
    this.name = 'NexApiError'
    this.status = status
    this.code = code
    this.turnId = turnId
    this.attachmentIndex = attachmentIndex
  }
}

export async function nexErrorFromResponse(res: Response): Promise<NexApiError> {
  const fallback = `http_${res.status}`
  let text = ''
  try {
    text = await res.text()
  } catch {
    return new NexApiError(res.status, fallback, `nex: HTTP ${res.status}`)
  }
  try {
    const body = JSON.parse(text) as { error?: unknown; code?: unknown; turn_id?: unknown; attachment_index?: unknown }
    if (typeof body.code === 'string' && body.code !== '') {
      const message = typeof body.error === 'string' && body.error !== '' ? body.error : `nex: HTTP ${res.status}`
      const turnId = typeof body.turn_id === 'string' && body.turn_id !== '' ? body.turn_id : undefined
      const idx = body.attachment_index
      const attachmentIndex = typeof idx === 'number' && Number.isInteger(idx) && idx >= 0 ? idx : undefined
      return new NexApiError(res.status, body.code, message, turnId, attachmentIndex)
    }
  } catch {
    // not JSON — fall through
  }
  return new NexApiError(res.status, fallback, `nex: HTTP ${res.status}${text ? `: ${text.slice(0, 200)}` : ''}`)
}
