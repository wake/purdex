// spa/src/lib/nex/worker-summary.ts — where a worker (execution) tab reads its
// summary: the live pane state when present, else the host's list row. The tab
// (`useTabDisplay`) and the notification dispatcher both go through these, so
// the tab title and the notification title cannot read different sources
// (worker-pane theme spec §8.2 / §8.4).
import { executionKey, useExecutionStore } from '../../stores/useExecutionStore'
import { useExecutionListStore } from '../../stores/useExecutionListStore'
import type { ExecutionState } from './event-reducer'
import type { HostListCaches } from './execution-list-effects'
import type { ExecutionSummary } from './types'
import { workerTabTitle } from './worker-tab-title'

/** The live summary (`useExecutionStore`), or null. Selector-safe. */
export function liveWorkerSummary(
  executions: Record<string, ExecutionState>, hostId: string, executionId: string,
): ExecutionSummary | null {
  return executions[executionKey(hostId, executionId)]?.summary ?? null
}

/** The host's list row (`useExecutionListStore`), or null. Selector-safe. */
export function rowWorkerSummary(byHost: HostListCaches, hostId: string, executionId: string): ExecutionSummary | null {
  return byHost[hostId]?.items.find((r) => r.id === executionId) ?? null
}

/** Imperative read, same order as the tab: live summary, else list row. */
export function readWorkerSummary(hostId: string, executionId: string): ExecutionSummary | null {
  return liveWorkerSummary(useExecutionStore.getState().executions, hostId, executionId)
    ?? rowWorkerSummary(useExecutionListStore.getState().byHost, hostId, executionId)
}

/**
 * The worker title from a pane's content and its summary (null when nothing
 * has text). Nexen `session_title` is not wired yet (phase E), so
 * `sessionTitle` stays undefined here — the one place to change when it is.
 */
export function workerTitleOf(
  content: { fromTitle?: string }, summary: Pick<ExecutionSummary, 'brief' | 'cwd'> | null | undefined,
): string | null {
  return workerTabTitle({ sessionTitle: undefined, fromTitle: content.fromTitle, brief: summary?.brief, cwd: summary?.cwd })
}
