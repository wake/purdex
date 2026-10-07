// spa/src/lib/nex/worker-summary.ts — where a worker (execution) tab reads its
// summary. The boundary: the single source is for STATUS-class data only —
// state, archived, pending_permission (the light, 等待核准), turn_count, the
// dedupe stamps, the transitions — and it is the host's list row, the one the
// worker projection reads too (useWorkerAgentProjection.ts); the live pane state
// only when the list is truncated and has no row for it (`hostListTruncated`).
// The TITLE is not status: it may fall back row → live pane state → the summary
// prefetched for it (#1557, worker-title-prefetch.ts), freshest first
// (`readWorkerSummary`). Never extend that fallback order to status. The tab
// (`useTabDisplay`) and the notification dispatcher both go through these, so
// the tab title and the notification title cannot read different sources
// (worker-pane theme spec §8.2 / §8.4).
import { executionKey, useExecutionStore } from '../../stores/useExecutionStore'
import { useExecutionListStore } from '../../stores/useExecutionListStore'
import { useWorkerTitlePrefetchStore } from '../../stores/useWorkerTitlePrefetchStore'
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

/**
 * The summary fetched only to title a worker that has neither of the above (`useWorkerTitlePrefetchStore`), or null.
 * A one-shot snapshot: for the title and the icon, never for what the worker is doing. Selector-safe.
 */
export function prefetchedWorkerSummary(
  byKey: Record<string, ExecutionSummary>, hostId: string, executionId: string,
): ExecutionSummary | null {
  return byKey[executionKey(hostId, executionId)] ?? null
}

/**
 * The host's list hit its page cap (D9): the newest rows may be missing, so a missing row says nothing. A worker's
 * STATUS is its list row; only then, with no row, its live summary (the projection's one fallback,
 * useWorkerAgentProjection.ts) — never the prefetch, a one-shot snapshot that does not say what the worker is doing.
 * Selector-safe.
 */
export function hostListTruncated(byHost: HostListCaches, hostId: string): boolean {
  return byHost[hostId]?.truncated === true
}

/**
 * Imperative read for a worker's TITLE (notification title, pane labels), same order as the tab's: the list row, else
 * the live summary, else the prefetch. Freshest first: the prefetch is fetched only while there is no live summary, and
 * a live entry is never dropped, so when both exist the live one is newer. A title is not status — the live summary
 * stays a fallback here, or a worker archived while its tab is open (no row, no prefetch) would lose its title. Never
 * read status (state, archived, pending_permission, turn_count) through this: status is the row alone.
 */
export function readWorkerSummary(hostId: string, executionId: string): ExecutionSummary | null {
  return rowWorkerSummary(useExecutionListStore.getState().byHost, hostId, executionId)
    ?? liveWorkerSummary(useExecutionStore.getState().executions, hostId, executionId)
    ?? prefetchedWorkerSummary(useWorkerTitlePrefetchStore.getState().byKey, hostId, executionId)
}

/**
 * 「等待核准」 (permission channel PC2, spec §5.4): the worker is waiting on a permission request. The summary
 * decides — the list row (`hostListTruncated` says when else), no event stream needed (Nexen contract §9.8 §2). `null` is an
 * answer (nothing pending); an absent field means a daemon older than Nexen v0.19.0, which is never awaiting.
 * Lifecycle-aware: archived, terminated, rejected and failed workers are never awaiting. The ONE shared definition.
 */
export function isAwaitingApproval(
  summary: Partial<Pick<ExecutionSummary, 'pending_permission' | 'state' | 'archived'>> | null | undefined,
): boolean {
  if (summary?.pending_permission == null) return false
  // An ended worker is never waiting: the summary can keep a stale pending request until the refetch lands.
  if (summary.archived) return false
  return summary.state !== 'terminated' && summary.state !== 'rejected' && summary.state !== 'failed'
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
