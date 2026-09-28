// spa/src/lib/nex/worker-agent-status.ts — projects a worker (exec) tab's
// state onto the same agent-light status definition terminal tabs use, so
// `tabIndicatorStyle`, `TabStatusIndicator`, `SubagentDots` and the rest of
// `useAgentStore`'s consumers are reused unchanged (spec §8.1–8.2).
import type { TurnOutcome } from './event-reducer'
import type { SubagentRef } from '../../stores/useAgentStore'

/**
 * The `useAgentStore` key namespace for a worker tab (spec §8.1): the agent
 * code is `exec-<executionId>`. It never collides with a tmux session code
 * (a 6-char base36 token, internal/module/session/codec.go, which never
 * contains '-'), and it carries no ':' so `splitCompositeKey` splits a worker
 * key at the last colon like any other.
 */
export const EXEC_PREFIX = 'exec-'

export function execAgentCode(executionId: string): string {
  return EXEC_PREFIX + executionId
}

export function isExecAgentCode(code: string): boolean {
  return code.startsWith(EXEC_PREFIX)
}

export function executionIdOfAgentCode(code: string): string | null {
  return isExecAgentCode(code) ? code.slice(EXEC_PREFIX.length) : null
}

/** `agentTypes[key]` provider → icon-set mapping (spec §8.1). */
export function providerAgentType(provider: string): string {
  if (provider === 'claude') return 'cc'
  if (provider === 'codex') return 'codex'
  return provider
}

export interface WorkerStatusInput {
  state: string
  turnLive: boolean
  lastOutcome: TurnOutcome | null
  hasTurn: boolean
  archived: boolean
  runningSubagents: { task_id: string; subagent_type?: string; started_at: number | null }[]
}

export interface WorkerProjection {
  status: 'running' | 'idle' | 'error' | 'clear'
  subagents: SubagentRef[]
}

/**
 * Worker state → the same agent-light status terminal tabs use (spec §8.2).
 * Rules apply in order, first match wins:
 * 1. `archived`, or execution `state === 'terminated'` → `clear`.
 * 2. `state === 'rejected'` (rejected before any turn ran) → `error`.
 * 3. a live turn, or `state` `queued`/`running` → `running`. This is also
 *    the error guard: it is checked before rule 4, so a stale `error` is
 *    replaced the moment the next accepted turn goes live.
 * 4. `lastOutcome === 'failed'`, or `state === 'failed'` → `error`.
 * 5–6. otherwise → `idle` — a resolved turn (`ok` or `interrupted`) and no
 *    turn yet both land here; the spec's two lines collapse to one branch.
 */
export function projectWorkerStatus(input: WorkerStatusInput): WorkerProjection {
  const subagents: SubagentRef[] = input.runningSubagents.map((s) => ({
    id: s.task_id,
    type: s.subagent_type ?? 'subagent',
    started_at: s.started_at ?? 0,
    source_pid: 0,
    source_start_time: '',
    is_proxy: false,
    delegating: false,
  }))

  let status: WorkerProjection['status']
  if (input.archived || input.state === 'terminated') {
    status = 'clear'
  } else if (input.state === 'rejected') {
    status = 'error'
  } else if (input.turnLive || input.state === 'queued' || input.state === 'running') {
    status = 'running'
  } else if (input.lastOutcome === 'failed' || input.state === 'failed') {
    status = 'error'
  } else {
    status = 'idle'
  }

  return { status, subagents }
}
