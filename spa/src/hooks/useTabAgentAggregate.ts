// spa/src/hooks/useTabAgentAggregate.ts — a tab's agent light over all its panes (U1-3c, spec N5), as a hook.
import { useMemo } from 'react'
import { useShallow } from 'zustand/react/shallow'
import { useAgentStore } from '../stores/useAgentStore'
import { useExecutionStore } from '../stores/useExecutionStore'
import { useExecutionListStore } from '../stores/useExecutionListStore'
import { aggregateTabAgents, type TabAgentAggregate, type TabAgentPane } from '../lib/agent-lights/tab-aggregate'
import { hostListTruncated, isAwaitingApproval, liveWorkerSummary, rowWorkerSummary } from '../lib/nex/worker-summary'
import type { HostListCaches } from '../lib/nex/execution-list-effects'
import type { ExecutionState } from '../lib/nex/event-reducer'

/**
 * One char per execution pane, from the host's list: '1' its row says awaiting, 'L' no row but the list is truncated
 * (the live summary decides), '0' otherwise. A string, so the selector result is a primitive (no re-render for an
 * unrelated change). Together with `liveAwaitingFlags` this is the status choice of the old primary-pane code
 * (`statusSummary = row ?? (truncated ? live : null)`) applied to every execution pane of the tab.
 */
export function listAwaitingFlags(byHost: HostListCaches, execPanes: TabAgentPane[]): string {
  return execPanes.map((p) => {
    const row = rowWorkerSummary(byHost, p.hostId, p.executionId!)
    if (row) return isAwaitingApproval(row) ? '1' : '0'
    return hostListTruncated(byHost, p.hostId) ? 'L' : '0'
  }).join('')
}

/** One char per execution pane from the live pane state: '1' awaiting. Only read where the list said 'L'. */
export function liveAwaitingFlags(executions: Record<string, ExecutionState>, execPanes: TabAgentPane[]): string {
  return execPanes.map((p) => (isAwaitingApproval(liveWorkerSummary(executions, p.hostId, p.executionId!)) ? '1' : '0')).join('')
}

/** The keys of the execution panes that await approval. */
export function awaitingKeys(execPanes: TabAgentPane[], listFlags: string, liveFlags: string): string[] {
  return execPanes.filter((_, i) => listFlags[i] === '1' || (listFlags[i] === 'L' && liveFlags[i] === '1')).map((p) => p.key)
}

/**
 * The tab's light over every agent pane of `panes` (from `tabAgentPanes`). One subscription per store; the results
 * are primitives or shallow-compared, so a frame for an unrelated session does not re-render the tab.
 */
export function useTabAgentAggregate(panes: TabAgentPane[]): TabAgentAggregate {
  const execPanes = useMemo(() => panes.filter((p) => p.executionId !== undefined), [panes])
  const listFlags = useExecutionListStore((s) => listAwaitingFlags(s.byHost, execPanes))
  const liveFlags = useExecutionStore((s) => (listFlags.includes('L') ? liveAwaitingFlags(s.executions, execPanes) : ''))
  const awaitingId = awaitingKeys(execPanes, listFlags, liveFlags).join('\n')
  const awaiting = useMemo(() => new Set(awaitingId ? awaitingId.split('\n') : []), [awaitingId])
  return useAgentStore(useShallow((s) => aggregateTabAgents(panes, s, awaiting)))
}
