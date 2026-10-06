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

/**
 * In-flight daemon requests by `<host>:<execution>`. Only the request is shared:
 * every caller awaits it and then runs its own forgetLease. (Not handoff.ts's
 * singleFlight: that rejects a second caller with handoff_in_progress, while a
 * joined exit must resolve with the same result.)
 */
const inFlight = new Map<string, Promise<NexExitWorkerResult>>()

/** Exit via a shared request; the list is refetched once per request. Throws HandoffApiError. */
export async function exitWorker(args: ExitWorkerArgs): Promise<NexExitWorkerResult> {
  const { hostId, executionId, leaseId, forgetLease } = args
  const key = `${hostId}:${executionId}`
  let req = inFlight.get(key)
  if (!req) {
    const created = (async () => {
      try {
        return await nexExitWorker(hostId, executionId, leaseId ? { lease_id: leaseId } : {})
      } finally {
        inFlight.delete(key)
        useExecutionListStore.getState().refetch(hostId)
      }
    })()
    inFlight.set(key, created)
    req = created
  }
  const result = await req
  forgetLease?.()
  return result
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
