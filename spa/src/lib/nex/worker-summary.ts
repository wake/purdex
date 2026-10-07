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
 * 「等待核准」 (permission channel PC2, spec §5.4): the worker is waiting on a permission request. The summary
 * decides — the list row or the live summary, no event stream needed (Nexen contract §9.8 §2). `null` is an
 * answer (nothing pending); an absent field means a daemon older than Nexen v0.19.0, which is never awaiting.
 */
export function isAwaitingApproval(summary: Pick<ExecutionSummary, 'pending_permission'> | null | undefined): boolean {
  return summary?.pending_permission != null
}

/**
 * The worker title from a pane's content and its summary (null when nothing
 * has text). `sessionTitle` is read from the summary only when the caller
 * says the host capability (`selectSessionTitleSupported`) is present —
 * older daemons never populate `session_title`, but gate on the capability
 * regardless so a title left over from a still-ready older cache is never
 * used either (worker-pane theme spec §0 fail-closed).
 */
export function workerTitleOf(
  content: { fromTitle?: string },
  summary: Pick<ExecutionSummary, 'brief' | 'cwd' | 'session_title'> | null | undefined,
  titleSupported: boolean,
): string | null {
  return workerTabTitle({
    sessionTitle: titleSupported ? summary?.session_title?.text : undefined,
    fromTitle: content.fromTitle,
    brief: summary?.brief,
    cwd: summary?.cwd,
  })
}
