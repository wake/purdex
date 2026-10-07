// spa/src/hooks/useExecutionActions.ts — the writes an execution pane issues
// (spec §4.3.3): send, interrupt. Every one goes through
// ensureLease(). Owns the restored draft after a failed send; ExecutionView
// composes this with the subscription/lease hooks and only renders.
import { useCallback, useLayoutEffect, useRef, useState } from 'react'
import { useExecutionStore, executionKey } from '../stores/useExecutionStore'
import { interruptExecution, sendMessage } from '../lib/nex/nex-api'
import { NexApiError } from '../lib/nex/types'
import type { ExecutionLeaseApi } from './useExecutionLease'
import type { WireImageAttachment } from '../lib/nex/worker-upload'
import { cancelSend, flushSend, holdSend } from '../lib/nex/send-buffer'
import { readWorkerDraft, workerDraftKey, writeWorkerDraft } from '../lib/nex/worker-draft-memory'

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
  /**
   * The send buffer (undo window): hold the message this many ms before it is
   * posted, so `cancelBuffered` can take it back. The optimistic line shows at
   * once; `handleSend` resolves true as soon as it is held. A typed send only —
   * ignored (sent at once) for `restoreDraft: false`.
   */
  bufferMs?: number
  /**
   * Buffered sends: how the held message ended — `sent` (the daemon accepted
   * it), `cancelled` (taken back; the text is restored as the draft) or
   * `failed` (the POST failed; likewise). The caller settles what it held
   * for the message (attachment chips) accordingly.
   */
  onBufferEnd?: (outcome: 'sent' | 'cancelled' | 'failed') => void
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
  /** Takes back the message held in the send buffer, if any (its text goes back to the box); false when none was held. */
  cancelBuffered(): boolean
  /** Put text back into the input for a send that failed before reaching `handleSend` (an image that could not be read). */
  restoreDraft(text: string): void
}

export function useExecutionActions(
  hostId: string,
  executionId: string,
  lease: Pick<ExecutionLeaseApi, 'ensureLease' | 'touch' | 'forget'>,
  /** The reply box's text right now: a restored message goes in front of it, never over it. */
  getBoxText?: () => string,
): ExecutionActions {
  const { ensureLease, touch, forget } = lease
  const key = executionKey(hostId, executionId)
  const [draft, setDraft] = useState<string | null>(null) // restored text after a failed send
  const [actionPending, setActionPending] = useState(false)
  // Monotonic send attempt counter. A send whose POST outlives its turn
  // (turn_stalled cleared the lock, the user sent again) must not touch the
  // newer send's bubble, lock, lastTurn, error or draft when it settles.
  const sendAttempt = useRef(0)
  // False once the pane is gone: a restore then goes to the draft memory, not to state nobody reads.
  const mounted = useRef(true)
  const getBoxTextRef = useRef(getBoxText)
  getBoxTextRef.current = getBoxText
  // A held (buffered) message must not be lost with the pane: unmounting
  // commits it at once, while this pane's lease is still held (layout cleanups
  // run before the lease hook's passive release). The POST then outlives the
  // pane; a failure lands in the draft memory (`restoreInto`).
  useLayoutEffect(() => {
    mounted.current = true
    return () => {
      flushSend(key)
      mounted.current = false
    }
  }, [key])

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

  // Puts a message back for the reader: as the draft (remounting the box) while
  // the pane is up, into the draft memory once it is gone. Whatever the box
  // holds now is kept after it, separated by a newline — never replaced.
  const restoreInto = useCallback((text: string) => {
    const draftKey = workerDraftKey(hostId, executionId)
    const live = mounted.current
    const current = (live ? getBoxTextRef.current?.() : readWorkerDraft(draftKey)) ?? ''
    const merged = current ? `${text}\n${current}` : text
    if (live) setDraft(merged)
    else writeWorkerDraft(draftKey, merged)
  }, [hostId, executionId])

  const post = useCallback(async (text: string, opts?: SendOptions, sentAt?: number): Promise<boolean> => {
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
    store().setPendingLocal(hostId, executionId, {
      text, delivery: null, ...(previews ? { attachments: previews } : {}), ...(sentAt !== undefined ? { sentAt } : {}),
    })
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
      // A line in the send buffer is a later message's, not this one's: leave it.
      const cur = store().executions[key]?.pendingLocal
      if (cur && !cur.buffered) {
        store().setPendingLocal(hostId, executionId, {
          text, delivery: r.delivery, ...(previews ? { attachments: previews } : {}), ...(sentAt !== undefined ? { sentAt } : {}),
        })
      }
      store().setLastTurn(hostId, executionId, { turnId: r.turn_id, delivery: r.delivery })
      // Accepted by the daemon: the box unlocks (the turn flag stays until its result).
      store().setSendLocked(hostId, executionId, false)
      return true
    } catch (e) {
      // Superseded: skip fail() too — a stale lease_* error must not forget
      // a lease the newer send may be using; that send reports its own.
      if (attempt !== sendAttempt.current) return false
      // …and a held later message keeps its line (the buffer settles it).
      if (!store().executions[key]?.pendingLocal?.buffered) store().setPendingLocal(hostId, executionId, null)
      store().setPendingSend(hostId, executionId, false)
      if (restoreDraft) restoreInto(opts?.draftText ?? text)
      fail(e)
      return false
    }
  }, [hostId, executionId, key, ensureLease, touch, fail, restoreInto])

  // Runs `go` once the box is unlocked (this execution's earlier POST accepted).
  const whenUnlocked = useCallback((go: () => void) => {
    if (!store().executions[key]?.sendLocked) { go(); return }
    const unsub = useExecutionStore.subscribe(
      (s) => s.executions[key]?.sendLocked ?? false,
      (locked) => { if (!locked) { unsub(); go() } },
    )
  }, [key])

  const bufferSend = useCallback((text: string, opts: SendOptions, ms: number) => {
    const restoreDraft = opts.restoreDraft ?? true
    // An earlier held message is committed first; its optimistic line gives way to this one.
    flushSend(key)
    store().setSendError(hostId, executionId, null)
    if (restoreDraft) setDraft(null)
    touch()
    const sentAt = Date.now()
    store().setPendingLocal(hostId, executionId, {
      text, delivery: null, ...(opts.previews ? { attachments: opts.previews } : {}), sentAt, buffered: { until: sentAt + ms },
    })
    holdSend(key, {
      // The POST waits for an earlier unaccepted one (the lock starts when it is issued, not while held).
      fire: () => whenUnlocked(() => {
        void post(text, opts, sentAt).then((ok) => opts.onBufferEnd?.(ok ? 'sent' : 'failed'))
      }),
      undo: () => {
        store().setPendingLocal(hostId, executionId, null)
        opts.onBufferEnd?.('cancelled')
        restoreInto(opts.draftText ?? text)
      },
    }, ms)
  }, [hostId, executionId, key, touch, post, whenUnlocked, restoreInto])

  const handleSend = useCallback(async (text: string, opts?: SendOptions): Promise<boolean> => {
    const ms = opts?.bufferMs ?? 0
    if (ms > 0 && (opts?.restoreDraft ?? true)) {
      bufferSend(text, opts!, ms)
      return true
    }
    // An immediate send (a quick reply, or the buffer off) goes after a message still held, never before it.
    if (flushSend(key)) await new Promise<void>((resolve) => whenUnlocked(resolve))
    return post(text, opts)
  }, [key, bufferSend, post, whenUnlocked])

  const cancelBuffered = useCallback(() => cancelSend(key), [key])

  const handleInterrupt = useCallback(async () => {
    touch()
    setActionPending(true)
    try { await interruptExecution(hostId, executionId, await ensureLease()) } catch (e) { fail(e) } finally { setActionPending(false) }
  }, [hostId, executionId, ensureLease, touch, fail])

  const restoreDraft = useCallback((text: string) => setDraft(text), [])

  return { draft, actionPending, handleSend, handleInterrupt, cancelBuffered, restoreDraft }
}
