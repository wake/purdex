// spa/src/lib/nex/nex-api.ts — typed REST client for the Nexen contract
// mounted at /api/nex on each pdx daemon (P-A spec §4.3). Every call goes
// through hostFetch (Bearer from the host store) plus the per-tab
// X-Pdx-Client header; tickets are never involved on this path.
import { hostFetch, pinnedHostFetch } from '../host-api'
import { useHostStore } from '../../stores/useHostStore'
import { getNexClientId } from './client-id'
import {
  nexErrorFromResponse,
  NexApiError,
  type AttachControlResponse,
  type AttachObserveResponse,
  type DelegateRequest,
  type DelegateResult,
  type EventsPage,
  type ExecutionSummary,
  type ExecutionsPage,
  type InterruptResponse,
  type NexCapabilities,
  type NexHostInfo,
  type SendResponse,
  type WorkerTask,
  type WorkerTasksSnapshot,
} from './types'
import { parseTask } from './tasks'
import { sanitizeSummaryRollup } from './validate-executions'
import type { WireImageAttachment } from './worker-upload'

const PREFIX = '/api/nex'

export function nexFetch(hostId: string, path: string, init?: RequestInit): Promise<Response> {
  // useHostStore.getDaemonBase() silently falls back to the active/first
  // host for an unknown hostId (spa/src/stores/useHostStore.ts) — a host
  // removed between the caller reading it and this call (e.g. mid-await in
  // ensureLease) must never ride that fallback to a different daemon (spec
  // §4.3.2 step 5: never fall back to another daemon). Refuse here, once,
  // instead of relying on every caller to re-check.
  if (!useHostStore.getState().hosts[hostId]) {
    return Promise.reject(new NexApiError(0, 'host_removed', 'host removed'))
  }
  const headers = new Headers(init?.headers)
  headers.set('X-Pdx-Client', getNexClientId())
  if (init?.body != null && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json')
  }
  // hostFetch rejects (TypeError, an aborted fetch, …) on anything that
  // never reached the server — offline, DNS failure, Tailscale path down.
  // Normalise it to the same NexApiError shape as a structured HTTP error
  // (I12) so every caller can switch on `code` instead of also handling a
  // bare rejection.
  return hostFetch(hostId, `${PREFIX}${path}`, { ...init, headers }).catch((e: unknown) => {
    const message = e instanceof Error ? e.message : String(e)
    throw new NexApiError(0, 'network', message)
  })
}

async function okJson<T>(res: Response): Promise<T> {
  if (!res.ok) throw await nexErrorFromResponse(res)
  return (await res.json()) as T
}

async function okVoid(res: Response): Promise<void> {
  if (!res.ok) throw await nexErrorFromResponse(res)
}

function postJson(hostId: string, path: string, body: unknown, method = 'POST', init?: RequestInit): Promise<Response> {
  return nexFetch(hostId, path, { ...init, method, body: JSON.stringify(body) })
}

function execPath(executionId: string, suffix = ''): string {
  return `/v1/executions/${encodeURIComponent(executionId)}${suffix}`
}

export function fetchNexCapabilities(hostId: string): Promise<NexCapabilities> {
  return nexFetch(hostId, '/v1/capabilities').then((r) => okJson<NexCapabilities>(r))
}

export function fetchNexHost(hostId: string): Promise<NexHostInfo> {
  return nexFetch(hostId, '/v1/host').then((r) => okJson<NexHostInfo>(r))
}

export interface ListExecutionsOptions {
  state?: string
  includeArchived?: boolean
  cursor?: string
  limit?: number
}

export function listExecutions(hostId: string, opts: ListExecutionsOptions = {}): Promise<ExecutionsPage> {
  const q = new URLSearchParams()
  if (opts.state) q.set('state', opts.state)
  if (opts.includeArchived) q.set('include_archived', 'true')
  if (opts.cursor) q.set('cursor', opts.cursor)
  if (opts.limit) q.set('limit', String(opts.limit))
  const qs = q.toString()
  return nexFetch(hostId, `/v1/executions${qs ? `?${qs}` : ''}`).then((r) => okJson<ExecutionsPage>(r))
}

export function delegateExecution(
  hostId: string,
  req: DelegateRequest,
  caps: Pick<NexCapabilities, 'delegate'> | null,
): Promise<DelegateResult> {
  // Fail-closed (spec F1): only an explicit `true` from the host's own
  // capabilities proves it will honour resume_session_id; anything else
  // (older daemon, capabilities not yet fetched) must not silently start a
  // fresh session under a brief that assumed continuity.
  if (req.resume_session_id !== undefined && caps?.delegate?.resume_session_id !== true) {
    return Promise.reject(new NexApiError(0, 'resume_unsupported', 'host does not support resume_session_id'))
  }
  const body: Record<string, unknown> = {
    provider: 'claude',
    brief: req.brief,
    mounts: [{ path: req.cwd, role: 'cwd', writable: true }],
  }
  if (req.profile !== undefined) body.sandbox_profile = req.profile
  if (req.labels !== undefined) body.labels = req.labels
  if (req.origin !== undefined) body.origin = req.origin
  if (req.resume_session_id !== undefined) body.resume_session_id = req.resume_session_id
  return postJson(hostId, '/v1/executions', body).then((r) => okJson<DelegateResult>(r))
}

export function getExecution(hostId: string, executionId: string): Promise<ExecutionSummary> {
  // R4 T4.1b: the rollup fields get the list rows' coercion before any view reads them.
  return nexFetch(hostId, execPath(executionId)).then((r) => okJson<unknown>(r)).then(sanitizeSummaryRollup)
}

/**
 * `GET /v1/executions/{id}/tasks` (capabilities.worker_rollup only — an older
 * daemon answers 404). Items go through `parseTask` with `startSeq = cursor`;
 * rows without a task_id are dropped, and a body that is not the documented
 * shape is an empty snapshot at cursor 0 (which drops nothing on merge).
 */
export async function fetchExecutionTasks(
  hostId: string,
  executionId: string,
  state: 'running' | 'all' = 'running',
): Promise<WorkerTasksSnapshot> {
  const body = await nexFetch(hostId, `${execPath(executionId, '/tasks')}?state=${state}`).then((r) => okJson<unknown>(r))
  const rec = typeof body === 'object' && body !== null ? (body as Record<string, unknown>) : {}
  // The cursor is an event seq: anything but a safe non-negative integer
  // makes the whole body malformed (its rows cannot be placed against seqs).
  if (!Number.isSafeInteger(rec.cursor) || (rec.cursor as number) < 0) return { items: [], cursor: 0 }
  const cursor = rec.cursor as number
  const items: WorkerTask[] = []
  if (Array.isArray(rec.items)) {
    for (const raw of rec.items) {
      const t = parseTask(raw, cursor)
      if (t) items.push(t)
    }
  }
  return { items, cursor }
}

export function fetchExecutionEvents(
  hostId: string,
  executionId: string,
  opts: { after: number; limit?: number },
): Promise<EventsPage> {
  const q = new URLSearchParams({ after: String(opts.after) })
  if (opts.limit) q.set('limit', String(opts.limit))
  return nexFetch(hostId, `${execPath(executionId, '/events')}?${q.toString()}`).then((r) => okJson<EventsPage>(r))
}

export function attachObserve(hostId: string, executionId: string): Promise<AttachObserveResponse> {
  return postJson(hostId, execPath(executionId, '/attach'), { mode: 'observe' }).then((r) => okJson<AttachObserveResponse>(r))
}

export function attachControl(hostId: string, executionId: string): Promise<AttachControlResponse> {
  return postJson(hostId, execPath(executionId, '/attach'), { mode: 'control' }).then((r) => okJson<AttachControlResponse>(r))
}

export function renewLease(hostId: string, executionId: string, leaseId: string): Promise<AttachControlResponse> {
  return postJson(hostId, execPath(executionId, '/attach/renew'), { lease_id: leaseId }).then((r) => okJson<AttachControlResponse>(r))
}

export function releaseLease(hostId: string, executionId: string, leaseId: string, init?: RequestInit): Promise<void> {
  return postJson(hostId, execPath(executionId, '/attach'), { lease_id: leaseId }, 'DELETE', init).then(okVoid)
}

/**
 * `releaseLease` for `hostId` as it is configured NOW: its endpoint and auth are read here, and the returned function
 * sends to them whenever it is called — also after the host has left the store, which `nexFetch` refuses. For the
 * host deletion, which may release a held lease only once the deletion has committed, and by then the row is gone
 * (host ownership plan §0.8). `null` for a host that is not configured: never the fallback host.
 */
export function pinnedLeaseRelease(hostId: string): ((executionId: string, leaseId: string) => Promise<void>) | null {
  const { hosts, getDaemonBase, getAuthHeaders } = useHostStore.getState()
  if (!Object.hasOwn(hosts, hostId)) return null
  const base = getDaemonBase(hostId)
  const auth = getAuthHeaders(hostId)
  return (executionId, leaseId) => {
    const headers = new Headers(auth)
    headers.set('X-Pdx-Client', getNexClientId())
    headers.set('Content-Type', 'application/json')
    return fetch(`${base}${PREFIX}${execPath(executionId, '/attach')}`, { method: 'DELETE', headers, body: JSON.stringify({ lease_id: leaseId }) })
      .catch((e: unknown) => {
        throw new NexApiError(0, 'network', e instanceof Error ? e.message : String(e))
      })
      .then(okVoid)
  }
}

/**
 * `attachments` (nexen contract §1.9) go on the wire only when non-empty, so a
 * text-only send is byte-identical to phase D. Key order is what
 * `requestBytes` measures. The caller has already feature-detected (§0).
 */
export function sendMessage(
  hostId: string, executionId: string, leaseId: string, text: string, attachments?: readonly WireImageAttachment[],
): Promise<SendResponse> {
  const body = attachments && attachments.length > 0 ? { lease_id: leaseId, text, attachments } : { lease_id: leaseId, text }
  return postJson(hostId, execPath(executionId, '/messages'), body).then((r) => okJson<SendResponse>(r))
}

export function interruptExecution(hostId: string, executionId: string, leaseId: string): Promise<InterruptResponse> {
  return postJson(hostId, execPath(executionId, '/interrupt'), { lease_id: leaseId }).then((r) => okJson<InterruptResponse>(r))
}

export function archiveExecution(hostId: string, executionId: string, undo = false): Promise<void> {
  // api/interact.go:81 archiveRequestWire — the flag is the target state.
  return postJson(hostId, execPath(executionId, '/archive'), { archived: !undo }).then(okVoid)
}

export function terminateExecution(hostId: string, executionId: string, leaseId: string): Promise<void> {
  return postJson(hostId, execPath(executionId, '/terminate'), { lease_id: leaseId }).then(okVoid)
}

export interface WorkerUploadResult {
  /** Absolute path on the daemon host, inside the execution's cwd. */
  path: string
  name: string
  size: number
}

/**
 * Save a file into the execution's cwd (worker-pane theme spec §9.1) so the
 * agent can Read it by path. A Purdex daemon route, not part of the Nexen
 * contract — hence `/api/nex/executions/…`, not `/api/nex/v1/…`, and not
 * `nexFetch`, which would stamp a JSON Content-Type over the multipart body.
 * `pinnedHostFetch` refuses a host this device lacks instead of falling back
 * to another daemon. Every failure rejects with a NexApiError: the daemon's
 * `code`, `network` for a request that never reached it, `host_removed` for
 * an unknown host — including one that disappears in the gap between this
 * function's own check and `pinnedHostFetch`'s (it rejects with a plain
 * `Error`, not a `NexApiError`, so the catch below re-checks the host store
 * rather than flattening that race into `network`).
 */
export async function uploadWorkerFile(hostId: string, executionId: string, file: File): Promise<WorkerUploadResult> {
  if (!useHostStore.getState().hosts[hostId]) throw new NexApiError(0, 'host_removed', 'host removed')
  const form = new FormData()
  form.append('file', file)
  let res: Response
  try {
    res = await pinnedHostFetch(hostId, `${PREFIX}/executions/${encodeURIComponent(executionId)}/uploads`, { method: 'POST', body: form })
  } catch (e) {
    if (!useHostStore.getState().hosts[hostId]) throw new NexApiError(0, 'host_removed', 'host removed')
    throw new NexApiError(0, 'network', e instanceof Error ? e.message : String(e))
  }
  const body = await okJson<Partial<WorkerUploadResult>>(res)
  if (typeof body.path !== 'string' || body.path === '') throw new NexApiError(res.status, 'bad_response', 'upload response has no path')
  return { path: body.path, name: typeof body.name === 'string' ? body.name : file.name, size: typeof body.size === 'number' ? body.size : file.size }
}

// Resolve an optional `host` hint (pane content / deeplink) onto a known SPA
// hostId; falls back to the first host so an execution always has a daemon
// to talk to. (Moved to resolve-host.ts, which P-B.2 keeps free of fetch
// imports so pane-utils / route code can use it without pulling in nex-api.)
export { resolveExecutionHostId } from './resolve-host'
