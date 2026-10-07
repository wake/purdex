// spa/src/hooks/useExecutionActions.ts — the writes an execution pane issues
// (spec §4.3.3): send, interrupt. Every one goes through
// ensureLease(). Owns the restored draft after a failed send; ExecutionView
// composes this with the subscription/lease hooks and only renders.
import { useCallback, useRef, useState } from 'react'
import { useExecutionStore, executionKey } from '../stores/useExecutionStore'
import { interruptExecution, sendMessage } from '../lib/nex/nex-api'
import { NexApiError } from '../lib/nex/types'
import type { ExecutionLeaseApi } from './useExecutionLease'
import type { WireImageAttachment } from '../lib/nex/worker-upload'

export interface SendOptions {
  /**
   * Default true: a failed send is put back into the input as the draft (a
   * typed send). False for a quick reply (R3 T2.1): the input is keyed on the
   * draft, so touching it at all would remount the input and wipe what the
   * user has half typed — the draft is neither cleared nor set, and a failure
   * only shows the send error.
   */
  restoreDraft?: boolean
  /**
   * The text restored as the draft after a failed send, when it differs from
   * the text sent — a message with attachments sends the typed text plus its
   * `[file: …]` lines, but only the typed text goes back into the input (the
   * chips stay on their own). Defaults to the sent text.
   */
  draftText?: string
  /** Native images (phase E), already encoded; forwarded to `sendMessage` as `attachments`. */
  attachments?: WireImageAttachment[]
  /**
   * The optimistic line's thumbnails for those images. The caller creates
   * the object URLs and hands them over: once on `pendingLocal` the store
   * revokes them when it drops this array (the same array is kept across the
   * delivery update); a re-entrant no-op send revokes them here.
   */
  previews?: { previewUrl: string; media_type: string }[]
}

export interface ExecutionActions {
  /** Text restored into the input after a failed send; null otherwise. */
  draft: string | null
  /** An interrupt request is in flight (sends are tracked by the store's `pendingSend`). */
  actionPending: boolean
  /**
   * Resolves true whenever the daemon accepted the message — including an
   * attempt superseded by a later send before it settled, so a caller that
   * clears state keyed on this send (e.g. the chips it composed in) still
   * does so. False only for the re-entrant no-op and on failure.
   */
  handleSend(text: string, opts?: SendOptions): Promise<boolean>
  handleInterrupt(): Promise<void>
  /** Put text back into the input for a send that failed before reaching `handleSend` (an image that could not be read). */
  restoreDraft(text: string): void
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
      store().setSendError(hostId, executionId, { code: e.code, message: e.message, turnId: e.turnId, attachmentIndex: e.attachmentIndex })
    } else {
      store().setSendError(hostId, executionId, { code: 'network', message: e instanceof Error ? e.message : String(e) })
    }
  }, [hostId, executionId, forget])

  const handleSend = useCallback(async (text: string, opts?: SendOptions): Promise<boolean> => {
    const restoreDraft = opts?.restoreDraft ?? true
    // Re-entrancy guard: sendLocked is set
    // synchronously below, before the `await ensureLease()`, so a second
    // submit fired while the first is still unaccepted (lease acquisition or
    // POST in flight) reads the lock here and is a no-op — the box shows it
    // as locked meanwhile. Once the daemon accepted the first, the lock is
    // released and a further send is allowed: the daemon queues it.
    if (store().executions[key]?.sendLocked) {
      // The previews never reach the store, whose write-drop revoke owns
      // them otherwise (useExecutionStore's revokeDroppedPreviews).
      for (const p of opts?.previews ?? []) URL.revokeObjectURL(p.previewUrl)
      return false
    }
    store().setSendError(hostId, executionId, null)
    if (restoreDraft) setDraft(null)
    touch()
    const previews = opts?.previews
    const attachments = opts?.attachments
    store().setPendingLocal(hostId, executionId, previews ? { text, delivery: null, attachments: previews } : { text, delivery: null })
    store().setPendingSend(hostId, executionId, true)
    store().setSendLocked(hostId, executionId, true)
    const attempt = ++sendAttempt.current
    try {
      const leaseId = await ensureLease()
      const r = attachments && attachments.length > 0
        ? await sendMessage(hostId, executionId, leaseId, text, attachments)
        : await sendMessage(hostId, executionId, leaseId, text)
      // Superseded but accepted: still true (the daemon has it) — only the
      // local bubble/lock/lastTurn writes below are skipped, since they'd
      // stomp the newer send's state.
      if (attempt !== sendAttempt.current) return true
      // execution.message_accepted (execution/service.go:794-807) can land
      // before this resolves and already clear pendingLocal + push the
      // durable bubble; writing it back unconditionally here would
      // resurrect a second bubble (I12, spec §5). Only write if the event hasn't
      // already consumed it.
      if (store().executions[key]?.pendingLocal) {
        store().setPendingLocal(hostId, executionId, previews ? { text, delivery: r.delivery, attachments: previews } : { text, delivery: r.delivery })
      }
      store().setLastTurn(hostId, executionId, { turnId: r.turn_id, delivery: r.delivery })
      // Accepted by the daemon: the box unlocks (the turn flag stays until its result).
      store().setSendLocked(hostId, executionId, false)
      return true
    } catch (e) {
      // Superseded: skip fail() too — a stale lease_* error must not forget
      // a lease the newer send may be using; that send reports its own.
      if (attempt !== sendAttempt.current) return false
      store().setPendingLocal(hostId, executionId, null)
      store().setPendingSend(hostId, executionId, false)
      if (restoreDraft) setDraft(opts?.draftText ?? text)
      fail(e)
      return false
    }
  }, [hostId, executionId, key, ensureLease, touch, fail])

  const handleInterrupt = useCallback(async () => {
    touch()
    setActionPending(true)
    try { await interruptExecution(hostId, executionId, await ensureLease()) } catch (e) { fail(e) } finally { setActionPending(false) }
  }, [hostId, executionId, ensureLease, touch, fail])

  const restoreDraft = useCallback((text: string) => setDraft(text), [])

  return { draft, actionPending, handleSend, handleInterrupt, restoreDraft }
}
