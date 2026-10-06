// spa/src/lib/nex/exit-worker.ts — "Exit worker" flow: ask the daemon to
// terminate the worker and archive its execution, then refresh the host's
// execution list so the row disappears.
import { useExecutionListStore } from '../../stores/useExecutionListStore'
import type { TFunction } from '../pane-labels'
import { HandoffApiError, nexExitWorker, type NexExitWorkerResult } from './handoff-api'

export interface ExitWorkerArgs {
  hostId: string
  executionId: string
  leaseId?: string
  /** Called once the daemon accepted the exit (the lease died with the worker). */
  forgetLease?: () => void
}

/** In-flight exits by `exit:<host>:<execution>`; a second call shares the first's promise. */
const inFlight = new Map<string, Promise<NexExitWorkerResult>>()

/** Single-flight exit; refetches the host's list after; throws HandoffApiError. */
export function exitWorker(args: ExitWorkerArgs): Promise<NexExitWorkerResult> {
  const { hostId, executionId, leaseId, forgetLease } = args
  const key = `exit:${hostId}:${executionId}`
  const existing = inFlight.get(key)
  if (existing) return existing
  const p = (async () => {
    try {
      const result = await nexExitWorker(hostId, executionId, leaseId ? { lease_id: leaseId } : {})
      forgetLease?.()
      return result
    } finally {
      inFlight.delete(key)
      useExecutionListStore.getState().refetch(hostId)
    }
  })()
  inFlight.set(key, p)
  return p
}

/** The toast text for a failed exit. */
export function exitErrorMessage(err: unknown, t: TFunction): string {
  if (err instanceof HandoffApiError) {
    if (err.code === 'held_by') {
      const p = err.body.principal
      return t('worker.exit.held_by', { principal: typeof p === 'string' && p !== '' ? p : '?' })
    }
    return t('worker.exit.failed', { reason: err.message || err.code })
  }
  return t('worker.exit.failed', { reason: String(err) })
}
