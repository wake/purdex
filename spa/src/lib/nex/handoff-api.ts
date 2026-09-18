// spa/src/lib/nex/handoff-api.ts — SPA wrappers for the daemon-orchestrated
// "hand to nex" / "take back to terminal" endpoints (P-C.3 spec §4.4,
// internal/module/nex/{handoff,takeback}.go). These live under
// /api/sessions/{code}/…, not /api/nex, so they go through hostFetch directly
// (Bearer from the host store) plus the same per-tab X-Pdx-Client principal
// the /api/nex path uses — the daemon derives the lease principal from it.
//
// Errors: every non-2xx body is `{error, code, …}` and the extra fields
// differ per code (reject_reason, rolled_back, session_id, step, principal,
// …), so the whole decoded body is kept on the error for the caller's
// message map instead of being flattened into a message string.
import { hostFetch } from '../host-api'
import { getNexClientId } from './client-id'

export class HandoffApiError extends Error {
  readonly status: number
  readonly code: string
  readonly body: Record<string, unknown>

  constructor(status: number, code: string, body: Record<string, unknown>, message?: string) {
    super(message ?? `handoff: HTTP ${status}`)
    this.name = 'HandoffApiError'
    this.status = status
    this.code = code
    this.body = body
  }
}

export interface NexHandoffRequest {
  expected_tmux_instance: string
  profile?: string
  /** Host resume template with `{id}` left unsubstituted; the daemon renders it. */
  rollback_command?: string
}

export interface NexHandoffResult {
  execution_id: string
  state: string
  effective_profile?: string
  session_id: string
  cwd: string
}

export interface NexTakebackRequest {
  expected_tmux_instance: string
  execution_id: string
  /** Host resume template with `{id}` left unsubstituted; the daemon renders it. */
  resume_command: string
  lease_id?: string
}

export interface NexTakebackResult {
  session_id: string
  archived: boolean
}

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

/**
 * Decode `{error, code, …}` into a HandoffApiError. A body that is not JSON
 * (or not an object) becomes `http_<status>` with an empty body; a JSON
 * object without a string `code` keeps its fields but also falls back to
 * `http_<status>` so callers never switch on a non-string.
 */
async function handoffErrorFromResponse(res: Response): Promise<HandoffApiError> {
  const fallback = `http_${res.status}`
  let text = ''
  try {
    text = await res.text()
  } catch {
    return new HandoffApiError(res.status, fallback, {})
  }
  let parsed: unknown
  try {
    parsed = JSON.parse(text)
  } catch {
    return new HandoffApiError(res.status, fallback, {})
  }
  if (!isRecord(parsed)) return new HandoffApiError(res.status, fallback, {})
  const code = typeof parsed.code === 'string' && parsed.code !== '' ? parsed.code : fallback
  const message = typeof parsed.error === 'string' && parsed.error !== '' ? parsed.error : undefined
  return new HandoffApiError(res.status, code, parsed, message)
}

/** Drop `undefined` optionals so they never reach the wire as `null`-ish noise. */
function compact(body: Record<string, unknown>): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  for (const [k, v] of Object.entries(body)) if (v !== undefined) out[k] = v
  return out
}

async function postSessionJson<T>(hostId: string, code: string, verb: string, body: Record<string, unknown>): Promise<T> {
  const headers = new Headers({ 'Content-Type': 'application/json', 'X-Pdx-Client': getNexClientId() })
  let res: Response
  try {
    res = await hostFetch(hostId, `/api/sessions/${encodeURIComponent(code)}/${verb}`, {
      method: 'POST',
      headers,
      body: JSON.stringify(compact(body)),
    })
  } catch (e: unknown) {
    // Never reached the server (offline, DNS, Tailscale path down): same
    // shape as a structured error so callers switch on `code` only.
    const message = e instanceof Error ? e.message : String(e)
    throw new HandoffApiError(0, 'network', {}, message)
  }
  if (!res.ok) throw await handoffErrorFromResponse(res)
  return (await res.json()) as T
}

/** `POST /api/sessions/{code}/nex-handoff` — exit CC in the pane and delegate to nex. */
export function nexHandoff(hostId: string, code: string, body: NexHandoffRequest): Promise<NexHandoffResult> {
  return postSessionJson<NexHandoffResult>(hostId, code, 'nex-handoff', { ...body })
}

/** `POST /api/sessions/{code}/nex-takeback` — settle the execution and resume CC in the pane. */
export function nexTakeback(hostId: string, code: string, body: NexTakebackRequest): Promise<NexTakebackResult> {
  return postSessionJson<NexTakebackResult>(hostId, code, 'nex-takeback', { ...body })
}
