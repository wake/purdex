// spa/src/lib/nex/permissions.ts — the permission requests a `handoff_ask`
// worker raised, folded from Nexen's `permission.requested` /
// `permission.resolved` events (permission channel plan Task 8; nexen
// capability-matrix §1.14 / §3, consumer-guide §9.8). Pure: the event reducer
// calls it after the global seq guard, like every other durable kind.
//
// Correlation is by `request_id` only, never by seq: a `permission.resolved`
// can land after the tool's own `tool_result`. Each request ends in exactly
// one resolution, so once an entry has ended nothing reopens it — not a
// `requested` re-delivered by a history/live overlap, not one that somehow
// arrives after its resolution.
import type { PermissionOutcome } from './types'

export type PermissionStatus = 'pending' | PermissionOutcome

export interface PermissionRequestState {
  requestId: string
  toolUseId?: string
  toolName: string
  displayName?: string
  description?: string
  /** The CLI's own input object, as asked (unbounded — the card bounds its preview). */
  input?: unknown
  decisionReason?: string
  blockedPath?: string
  /** Set when a subagent asks; equals that subagent's `task_started.task_id`. */
  agentId?: string
  /** `created_at` of the `permission.requested` event (ms). */
  requestedAt: number
  status: PermissionStatus
  /** `expired` only: the timeout that denied it, in seconds. */
  timeoutS?: number
  /** `cancelled` only: `interrupt` / `cli_cancelled` / `turn_ended` / `daemon_restart`. */
  reason?: string
}

export type PermissionTable = Record<string, PermissionRequestState>

const OUTCOMES: ReadonlySet<string> = new Set<PermissionOutcome>(['allowed', 'denied', 'cancelled', 'expired'])

export function isPermissionEventKind(kind: string): boolean {
  return kind === 'permission.requested' || kind === 'permission.resolved'
}

function str(p: Record<string, unknown>, k: string): string | undefined {
  const v = p[k]
  return typeof v === 'string' && v !== '' ? v : undefined
}

/** The request's details from a `permission.requested` payload; absent keys stay absent. */
function details(p: Record<string, unknown>): Omit<PermissionRequestState, 'requestId' | 'requestedAt' | 'status'> {
  const d: Omit<PermissionRequestState, 'requestId' | 'requestedAt' | 'status'> = { toolName: str(p, 'tool_name') ?? '' }
  const toolUseId = str(p, 'tool_use_id')
  if (toolUseId) d.toolUseId = toolUseId
  const displayName = str(p, 'display_name')
  if (displayName) d.displayName = displayName
  const description = str(p, 'description')
  if (description) d.description = description
  if (p.input !== undefined) d.input = p.input
  const decisionReason = str(p, 'decision_reason')
  if (decisionReason) d.decisionReason = decisionReason
  const blockedPath = str(p, 'blocked_path')
  if (blockedPath) d.blockedPath = blockedPath
  const agentId = str(p, 'agent_id')
  if (agentId) d.agentId = agentId
  return d
}

/**
 * Fold one permission event into the table. Returns the same table object when
 * nothing changes (a duplicate, a malformed payload), so the caller can tell.
 *
 * - `requested` creates a `pending` entry. An entry that already exists keeps
 *   its status: a duplicate of a pending one is a no-op, and an ended one is
 *   only completed with the details it lacked (it was first seen through its
 *   resolution) — never reopened.
 * - `resolved` sets the status from the closed `outcome` set (`timeout_s` on
 *   `expired`, `reason` on `cancelled`). A resolution for a request this state
 *   never saw is recorded as ended, so its `requested` cannot reopen it later.
 */
export function applyPermissionEvent(table: PermissionTable, kind: string, p: Record<string, unknown>, at: number): PermissionTable {
  const requestId = str(p, 'request_id')
  if (!requestId) return table
  const cur = table[requestId]
  if (kind === 'permission.requested') {
    if (cur?.status === 'pending') return table
    if (cur) return cur.toolName ? table : { ...table, [requestId]: { ...cur, ...details(p), status: cur.status } }
    return { ...table, [requestId]: { requestId, ...details(p), requestedAt: at, status: 'pending' } }
  }
  const outcome = str(p, 'outcome')
  if (!outcome || !OUTCOMES.has(outcome)) return table
  const status = outcome as PermissionOutcome
  const timeoutS = status === 'expired' && typeof p.timeout_s === 'number' && Number.isFinite(p.timeout_s) ? p.timeout_s : undefined
  const reason = status === 'cancelled' ? str(p, 'reason') : undefined
  if (cur && cur.status === status && cur.timeoutS === timeoutS && cur.reason === reason) return table
  const base: PermissionRequestState = cur ?? { requestId, toolName: '', requestedAt: at, status }
  const next: PermissionRequestState = { ...base, status }
  if (timeoutS !== undefined) next.timeoutS = timeoutS
  if (reason !== undefined) next.reason = reason
  return { ...table, [requestId]: next }
}

/**
 * The request the pane answers next: the pending one asked earliest (ties by
 * request_id) — never derived from seq. `exclude` skips requests the pane
 * already closed itself (its answer landed, or it learned the request had
 * ended) while their resolution is still on the way.
 */
export function selectPendingPermission(s: { permissions: PermissionTable }, exclude?: ReadonlySet<string>): PermissionRequestState | undefined {
  let best: PermissionRequestState | undefined
  for (const r of Object.values(s.permissions)) {
    if (r.status !== 'pending' || exclude?.has(r.requestId)) continue
    if (!best || r.requestedAt < best.requestedAt || (r.requestedAt === best.requestedAt && r.requestId < best.requestId)) best = r
  }
  return best
}
