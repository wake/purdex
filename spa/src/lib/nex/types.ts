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
  // Single-get only (GET /v1/executions/{id}); absent on list rows.
  turn_count?: number
  live_turn_id?: string
  lease?: ExecutionLeaseView
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
  send: { delivery: string[]; max_text_bytes: number }
  brief?: { max_bytes: number }
  origin?: { max_bytes: number }
  labels?: {
    max_count: number
    max_key_bytes: number
    max_value_bytes: number
    max_total_bytes: number
    reserved_prefix: string
  }
  delegate?: { resume_session_id?: boolean }
  /**
   * Presence = the daemon emits the N2 `tool_use` / `tool_result` events
   * (nexen contract §0). The exec pane does NOT branch on it (P-B3 spec
   * §4.1: N2 is an overlay on the raw frames, never a switch). The numbers
   * are the daemon's actual caps for `output.text` / `diff.hunks`; a client
   * that shows "showing X of Y" must read them from here and never hard-code
   * them (P-B3 spec §4.5).
   */
  tool_events?: { output_max_bytes: number; diff_max_lines: number }
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

  constructor(status: number, code: string, message: string, turnId?: string) {
    super(message)
    this.name = 'NexApiError'
    this.status = status
    this.code = code
    this.turnId = turnId
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
    const body = JSON.parse(text) as { error?: unknown; code?: unknown; turn_id?: unknown }
    if (typeof body.code === 'string' && body.code !== '') {
      const message = typeof body.error === 'string' && body.error !== '' ? body.error : `nex: HTTP ${res.status}`
      const turnId = typeof body.turn_id === 'string' && body.turn_id !== '' ? body.turn_id : undefined
      return new NexApiError(res.status, body.code, message, turnId)
    }
  } catch {
    // not JSON — fall through
  }
  return new NexApiError(res.status, fallback, `nex: HTTP ${res.status}${text ? `: ${text.slice(0, 200)}` : ''}`)
}
