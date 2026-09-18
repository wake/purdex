// spa/src/hooks/useExecutionActions.ts — the writes an execution pane issues
// (spec §4.3.3): send, interrupt, terminate. Every one goes through
// ensureLease(). Owns the restored draft after a failed send; ExecutionView
// composes this with the subscription/lease hooks and only renders.
import { useCallback, useRef, useState } from 'react'
import { useExecutionStore, executionKey } from '../stores/useExecutionStore'
import { interruptExecution, sendMessage, terminateExecution } from '../lib/nex/nex-api'
import { NexApiError } from '../lib/nex/types'
import type { ExecutionLeaseApi } from './useExecutionLease'

export interface ExecutionActions {
  /** Text restored into the input after a failed send; null otherwise. */
  draft: string | null
  /** An interrupt or terminate request is in flight (sends are tracked by the store's `pendingSend`). */
  actionPending: boolean
  handleSend(text: string): Promise<void>
  handleInterrupt(): Promise<void>
  handleTerminate(): Promise<void>
}

export function useExecutionActions(
  hostId: string,
  executionId: string,
  lease: Pick<ExecutionLeaseApi, 'ensureLease' | 'touch' | 'forget'>,
): ExecutionActions {
  const { ensureLease, touch, forget } = lease
  const key = executionKey(hostId, executionId)
  const [draft, setDraft] = useState<string | null>(null) // restored text after a failed send
  const [actionPending, setActionPending] = useState(false)
  // Monotonic send attempt counter. A send whose POST outlives its turn
  // (turn_stalled cleared the lock, the user sent again) must not touch the
  // newer send's bubble, lock, lastTurn, error or draft when it settles.
  const sendAttempt = useRef(0)

  const store = () => useExecutionStore.getState()
  // A fetch that never reached the daemon rejects with a TypeError, not a
  // NexApiError; I12 still wants a code, so map it (`network`).
  const fail = useCallback((e: unknown) => {
    if (e instanceof NexApiError) {
      // no_live_turn / lease_abandoned are silent; lease_held is already
      // surfaced by the dedicated notice (ensureLease wrote leaseError
      // before rethrowing) — a second "Send failed" banner would be
      // redundant and there is no execution.error.lease_held copy for it.
      if (e.code === 'no_live_turn' || e.code === 'lease_abandoned' || e.code === 'lease_held') return
      // The server already invalidated this lease — drop it locally too
      // (no release() DELETE, it's pointless) so the next send/interrupt/
      // terminate re-acquires instead of retrying against a dead lease id.
      if (e.code === 'lease_expired' || e.code === 'lease_mismatch' || e.code === 'lease_required') forget()
      store().setSendError(hostId, executionId, { code: e.code, message: e.message, turnId: e.turnId })
    } else {
      store().setSendError(hostId, executionId, { code: 'network', message: e instanceof Error ? e.message : String(e) })
    }
  }, [hostId, executionId, forget])

  const handleSend = useCallback(async (text: string) => {
    // Re-entrancy guard: pendingSend is set
    // synchronously below, before the `await ensureLease()`, so a second
    // submit fired while the first lease acquisition is still in flight
    // reads the lock here and is a no-op — without this, a slow lease let
    // two sends race and both post (sharing the same pendingLocal bubble).
    if (store().executions[key]?.pendingSend) return
    store().setSendError(hostId, executionId, null)
    setDraft(null)
    touch()
    store().setPendingLocal(hostId, executionId, { text, delivery: null })
    store().setPendingSend(hostId, executionId, true)
    const attempt = ++sendAttempt.current
    try {
      const leaseId = await ensureLease()
      const r = await sendMessage(hostId, executionId, leaseId, text)
      if (attempt !== sendAttempt.current) return
      // execution.message_accepted (execution/service.go:794-807) can land
      // before this resolves and already clear pendingLocal + push the
      // durable bubble; writing it back unconditionally here would
      // resurrect a second bubble (I12, spec §5). Only write if the event hasn't
      // already consumed it.
      if (store().executions[key]?.pendingLocal) {
        store().setPendingLocal(hostId, executionId, { text, delivery: r.delivery })
      }
      store().setLastTurn(hostId, executionId, { turnId: r.turn_id, delivery: r.delivery })
    } catch (e) {
      // Superseded: skip fail() too — a stale lease_* error must not forget
      // a lease the newer send may be using; that send reports its own.
      if (attempt !== sendAttempt.current) return
      store().setPendingLocal(hostId, executionId, null)
      store().setPendingSend(hostId, executionId, false)
      setDraft(text)
      fail(e)
    }
  }, [hostId, executionId, key, ensureLease, touch, fail])

  const handleInterrupt = useCallback(async () => {
    touch()
    setActionPending(true)
    try { await interruptExecution(hostId, executionId, await ensureLease()) } catch (e) { fail(e) } finally { setActionPending(false) }
  }, [hostId, executionId, ensureLease, touch, fail])

  const handleTerminate = useCallback(async () => {
    touch()
    setActionPending(true)
    try { await terminateExecution(hostId, executionId, await ensureLease()) } catch (e) { fail(e) } finally { setActionPending(false) }
  }, [hostId, executionId, ensureLease, touch, fail])

  return { draft, actionPending, handleSend, handleInterrupt, handleTerminate }
}
