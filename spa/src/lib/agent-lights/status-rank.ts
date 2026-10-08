// spa/src/lib/agent-lights/status-rank.ts — the one priority order of agent statuses (spec N5): the tab light over its
// panes (tab-aggregate.ts) and the workspace indicators over its tabs (workspace-indicators.ts) both rank with it.
import type { AgentStatus } from '../../stores/useAgentStore'

/** error > waiting > running > idle > none. */
export const STATUS_RANK: Record<AgentStatus, number> = {
  error: 4,
  waiting: 3,
  running: 2,
  idle: 1,
}

/** 0 for a code with no status. */
export function statusRank(s: AgentStatus | undefined): number {
  return s === undefined ? 0 : STATUS_RANK[s]
}
