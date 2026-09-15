// spa/src/lib/nex/nex-api.ts — typed REST client for the Nexen contract
// mounted at /api/nex on each pdx daemon (P-A spec §4.3). Every call goes
// through hostFetch (Bearer from the host store) plus the per-tab
// X-Pdx-Client header; tickets are never involved on this path.
import { hostFetch } from '../host-api'
import { useHostStore } from '../../stores/useHostStore'
import { getNexClientId } from './client-id'
import {
  nexErrorFromResponse,
  NexApiError,
  type AttachControlResponse,
  type AttachObserveResponse,
  type EventsPage,
  type ExecutionSummary,
  type ExecutionsPage,
  type InterruptResponse,
  type NexCapabilities,
  type NexHostInfo,
  type SendResponse,
} from './types'

const PREFIX = '/api/nex'

export function nexFetch(hostId: string, path: string, init?: RequestInit): Promise<Response> {
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

function postJson(hostId: string, path: string, body: unknown, method = 'POST'): Promise<Response> {
  return nexFetch(hostId, path, { method, body: JSON.stringify(body) })
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

export function getExecution(hostId: string, executionId: string): Promise<ExecutionSummary> {
  return nexFetch(hostId, execPath(executionId)).then((r) => okJson<ExecutionSummary>(r))
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

export function releaseLease(hostId: string, executionId: string, leaseId: string): Promise<void> {
  return postJson(hostId, execPath(executionId, '/attach'), { lease_id: leaseId }, 'DELETE').then(okVoid)
}

export function sendMessage(hostId: string, executionId: string, leaseId: string, text: string): Promise<SendResponse> {
  return postJson(hostId, execPath(executionId, '/messages'), { lease_id: leaseId, text }).then((r) => okJson<SendResponse>(r))
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

/**
 * Resolve an optional `host` hint (pane content / deeplink) onto a known SPA
 * hostId; falls back to the first host so an execution always has a daemon
 * to talk to. (Moved from the M0 execution-api.ts, which P-B.2 deletes.)
 */
export function resolveExecutionHostId(host?: string): string {
  const { hostOrder } = useHostStore.getState()
  if (host && hostOrder.includes(host)) return host
  return hostOrder[0] ?? ''
}
