// spa/src/lib/nex/worker-rebuild.ts — "Rebuild as worker" from an ended worker
// pane: ask the daemon to start a new worker that resumes the conversation, then
// point the pane at the new execution. Single-flight per pane (a double click is
// one request); the swap is a checked one, like handoff.ts's.
import { useTabStore } from '../../stores/useTabStore'
import { useUndoToast } from '../../stores/useUndoToast'
import { useExecutionListStore } from '../../stores/useExecutionListStore'
import { isRefShownNow } from '../shown-hosts'
import type { TFunction } from '../pane-labels'
import type { PaneContent } from '../../types/tab'
import { HandoffApiError, nexWorkerRebuild, type NexWorkerRebuildResult } from './handoff-api'
import { ASK_PROFILE, executionContentFor, handoffErrorMessage, permissionTimeoutFor, singleFlight } from './handoff'

export interface RebuildAsWorkerArgs {
  hostId: string
  sessionId: string
  cwd: string
  profile?: string
  replaceExecutionId?: string
  tabId: string
  paneId: string
  /** CAS on the pane's current content (trySetPaneContent). */
  expect: (c: PaneContent) => boolean
}

/**
 * Single-flight `rebuild:${hostId}:${paneId}` (a second caller is refused with
 * `handoff_in_progress`). On 200 the pane swaps to the new execution — also when
 * it was rejected, which the pane then shows as "start failed".
 */
export function rebuildAsWorker(args: RebuildAsWorkerArgs): Promise<{ result: NexWorkerRebuildResult; swapped: boolean }> {
  const { hostId, sessionId, cwd, profile, replaceExecutionId, tabId, paneId, expect } = args
  return singleFlight(`rebuild:${hostId}:${paneId}`, async () => {
    // A rebuild keeps the mode: an asking row also gets the current approval timeout (permission channel plan Task 7).
    const timeout = profile === ASK_PROFILE ? permissionTimeoutFor(hostId) : undefined
    const result = await nexWorkerRebuild(hostId, {
      session_id: sessionId,
      cwd,
      ...(profile ? { profile } : {}),
      ...(replaceExecutionId ? { replace_execution_id: replaceExecutionId } : {}),
      ...(timeout !== undefined ? { permission_timeout_s: timeout } : {}),
    })
    const swapped = isRefShownNow(hostId)
      && useTabStore.getState().trySetPaneContent(tabId, paneId, executionContentFor(hostId, result.execution_id), expect)
    return { result, swapped }
  })
}

/** The inline text for a failed rebuild. */
export function rebuildErrorMessage(err: unknown, t: TFunction): string {
  if (err instanceof HandoffApiError) {
    if (err.code === 'session_owned') {
      return t(err.body.owner === 'worker' ? 'worker.rebuild.owned_worker' : 'worker.rebuild.owned_terminal')
    }
    return t('worker.rebuild.failed', { reason: handoffErrorMessage(t, err) })
  }
  return t('worker.rebuild.failed', { reason: String(err) })
}

/**
 * After a rebuild that returned: when the pane could not be swapped (it moved on
 * while the request ran) the worker exists anyway — say where to find it.
 */
export function announceRebuildOutcome(t: TFunction, hostId: string, outcome: { swapped: boolean }): void {
  if (outcome.swapped) return
  useUndoToast.getState().show(t('worker.rebuild.no_pane'))
  useExecutionListStore.getState().refetch(hostId)
}
