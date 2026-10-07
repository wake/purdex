// spa/src/hooks/usePermissionAnswer.ts — answering a `handoff_ask` worker's
// permission request from its pane (permission channel plan Task 9; spec
// §5.3; nexen consumer-guide §9.8 / capability-matrix §1.14). The answer goes
// through the pane's own control lease — the one `send` uses — so this hook
// takes the pane's lease API rather than holding a lease of its own.
//
// What an answer meets:
// - 200 → the request is closed here at once; its `permission.resolved`
//   follows on the stream.
// - 409 `permission_not_pending` → it had already ended (answered elsewhere,
//   withdrawn, expired, the turn or worker ended): close quietly, never retry —
//   also when it is the answer re-sent after a lease re-attach.
// - `lease_expired` / `lease_mismatch` / `lease_required` → nothing was sent:
//   drop the lease, attach again, and answer the SAME request once more; a
//   second failure is shown.
// - `permission_not_found` is NOT quiet: the execution never had that id, so
//   the pane used a wrong one — shown and warned, a bug to surface.
// - `lease_held` / `lease_abandoned` stay silent here, as for a send: the
//   pane's lease notice already says who holds it.
//
// "Closed here" outlives the pane (A2): a tab switch right after an answer,
// before its `permission.resolved` arrives, unmounts the pane while the store
// still reads pending. The mark lives in `lib/nex/permission-card-memory`, read
// on mount and on every answer; ExecutionView prunes it with the card drafts.
import { useCallback, useRef, useState } from 'react'
import { answerPermission } from '../lib/nex/nex-api'
import { NexApiError } from '../lib/nex/types'
import type { PermissionRequestState } from '../lib/nex/permissions'
import { closePermissionCard, closedPermissionRequests, isPermissionCardClosed, permissionCardKey } from '../lib/nex/permission-card-memory'
import { useNexHostStore } from '../stores/useNexHostStore'
import type { ExecutionLeaseApi } from './useExecutionLease'

export interface PermissionAnswerError {
  /** The request the failed answer was for — the card shows it only on that request. */
  requestId: string
  code: string
  message: string
  /** `invalid_permission_answer`: the field to fix (`message` = the deny note). */
  field?: string
}

export interface PermissionAnswerApi {
  /** Never rejects; the outcome lands in `closed` / `error`. A call while one is in flight is a no-op. */
  answer(req: Pick<PermissionRequestState, 'requestId'>, decision: 'allow' | 'deny', message?: string): Promise<void>
  /** An answer is in flight. */
  busy: boolean
  error?: PermissionAnswerError
  /** Requests this pane closed itself (answered, or found already ended) whose resolution may still be on the way. */
  closed: ReadonlySet<string>
}

const LEASE_LOST = new Set(['lease_expired', 'lease_mismatch', 'lease_required'])
const SILENT = new Set(['lease_held', 'lease_abandoned'])

function codeOf(e: unknown): string {
  return e instanceof NexApiError ? e.code : 'network'
}

export function usePermissionAnswer(
  hostId: string,
  executionId: string,
  lease: Pick<ExecutionLeaseApi, 'ensureLease' | 'forget' | 'touch'>,
): PermissionAnswerApi {
  const { ensureLease, forget, touch } = lease
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<PermissionAnswerError | undefined>(undefined)
  // The render copy of the memory's marks for this execution (the memory is not reactive). A mark the pane's
  // pruning drops later may linger here; it only ever names a request the store no longer lists as pending, or
  // one of a worker that has ended (whose pane is the ended screen, with no card).
  const [closed, setClosed] = useState<ReadonlySet<string>>(() => closedPermissionRequests(hostId, executionId))
  // Same-tick guard: a double click reaches here twice before `busy` renders.
  const inFlight = useRef(false)

  const close = useCallback((requestId: string) => {
    closePermissionCard(permissionCardKey(hostId, executionId, requestId))
    setClosed(closedPermissionRequests(hostId, executionId))
  }, [hostId, executionId])

  const answer = useCallback(async (req: Pick<PermissionRequestState, 'requestId'>, decision: 'allow' | 'deny', message?: string) => {
    const { requestId } = req
    if (inFlight.current || isPermissionCardClosed(permissionCardKey(hostId, executionId, requestId))) return
    inFlight.current = true
    setBusy(true)
    setError(undefined)
    touch()
    const note = decision === 'deny' ? message?.trim() : undefined
    const send = async () => {
      const leaseId = await ensureLease()
      // Read at call time: the cached capabilities name the answer route (nex-api cannot import the store).
      const caps = useNexHostStore.getState().byHost[hostId]?.capabilities
      return answerPermission(hostId, executionId, requestId, note ? { decision, message: note, leaseId } : { decision, leaseId }, caps)
    }
    try {
      try {
        await send()
      } catch (e) {
        if (!LEASE_LOST.has(codeOf(e))) throw e
        // The refused answer sent nothing: take a fresh lease and answer the same request once more.
        forget()
        await send()
      }
      close(requestId)
    } catch (e) {
      const code = codeOf(e)
      if (code === 'permission_not_pending') {
        close(requestId)
      } else if (!SILENT.has(code)) {
        if (LEASE_LOST.has(code)) forget()
        if (code === 'permission_not_found') {
          console.warn(`[permission] ${requestId} is not a request of execution ${executionId} (permission_not_found)`)
        }
        setError({
          requestId,
          code,
          message: e instanceof Error ? e.message : String(e),
          ...(e instanceof NexApiError && e.field ? { field: e.field } : {}),
        })
      }
    } finally {
      inFlight.current = false
      setBusy(false)
    }
  }, [hostId, executionId, ensureLease, forget, touch, close])

  return { answer, busy, error, closed }
}
