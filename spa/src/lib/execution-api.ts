// spa/src/lib/execution-api.ts — read-only client for the daemon execution
// projection (M0 dispatch, Task P.12). This is strictly an observe path: it
// only issues GET /api/execution/{id} and never opens a stdin write channel.
import { useHostStore } from '../stores/useHostStore'
import { hostFetch } from './host-api'

/** One artifact's pointer-first projection (spec §6): opaque pointer + summary. */
export interface ExecutionArtifact {
  kind: string
  pointer: string
  meta?: Record<string, unknown>
}

/** The daemon's read-only execution projection (GET /api/execution/{id}). */
export interface ExecutionView {
  execution_id: string
  dispatch_id: string
  status: string
  launch_state: string
  /** Null until the execution has launched a tmux session (deeplink handle). */
  session_code: string | null
  session_name: string
  host_id?: string
  provider: string
  attempt_no: number
  repo_location: string
  head_at_start: string
  dirty_at_start: boolean
  sandbox_profile: string
  outcome_source?: string
  artifacts?: ExecutionArtifact[]
  created_at: number
  updated_at: number
}

/**
 * Resolve the deeplink `host` hint onto a known SPA hostId. The hint is optional
 * (single-daemon M0 can omit it); when it names a registered host we use it,
 * otherwise we fall back to the first host — so an execution always has a daemon
 * to query even when the hint is absent or stale.
 */
export function resolveExecutionHostId(host?: string): string {
  const { hostOrder } = useHostStore.getState()
  if (host && hostOrder.includes(host)) return host
  return hostOrder[0] ?? ''
}

/**
 * Fetch the execution projection from the daemon. Returns null on 404 (unknown
 * execution) so callers can render a stable "not found" landing rather than
 * throwing. Any other non-2xx is a real error and rejects.
 */
export async function fetchExecutionView(
  hostId: string,
  executionId: string,
): Promise<ExecutionView | null> {
  const res = await hostFetch(hostId, `/api/execution/${encodeURIComponent(executionId)}`)
  if (res.status === 404) return null
  if (!res.ok) throw new Error(`execution fetch failed: ${res.status}`)
  return (await res.json()) as ExecutionView
}
